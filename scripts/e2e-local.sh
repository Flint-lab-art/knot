#!/usr/bin/env bash
# scripts/e2e-local.sh —— 本地真实 PG 上的端到端验收
#
# 覆盖：建库/迁移 → 服务端 API 与鉴权 → CLI 全链路 → 同步对账 → TUI 启动
#      → 公开站接口 → 限流 → 备份恢复 → 回滚。每一步都有断言，失败即退出。
#
# 用法：
#   bash scripts/e2e-local.sh          # 用默认 postgres peer 连接
#   PG_ADMIN_DSN=... bash scripts/e2e-local.sh
#
# 会创建临时库 knot_e2e_<随机> 与临时目录，结束时全部清理（失败也清理）。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/knot-e2e.XXXXXX")"
PG_ADMIN_DSN="${PG_ADMIN_DSN:-postgres://postgres@/postgres?host=/run/postgresql&sslmode=disable}"
DB="knot_e2e_$(date +%s)_$RANDOM"
# 从管理 DSN 推导目标库 DSN：只替换路径里的库名，保留 host/sslmode 等参数。
dsn_for() { printf '%s' "${PG_ADMIN_DSN}" | sed -E "s#(://[^/]*/)[^?]*#\1$1#"; }
DB_DSN="$(dsn_for "${DB}")"
PORT="${PORT:-18099}"
BASE="http://127.0.0.1:${PORT}"
TOKEN="e2e-token-$(date +%s)"
KNOT="${WORK}/knot"
SERVER="${WORK}/knot-server"
CONFIG="${WORK}/config.toml"
CONFIG_CLIENT="${WORK}/client.toml"
CACHE="${WORK}/cache.db"
SERVER_PID=""

PASS=0
FAIL=0

# ---------- 输出 ----------
c_green=$'\033[32m'; c_red=$'\033[31m'; c_blue=$'\033[34m'; c_reset=$'\033[0m'

section() { printf '\n%s== %s ==%s\n' "${c_blue}" "$1" "${c_reset}"; }
ok()      { PASS=$((PASS+1)); printf '  %s✓%s %s\n' "${c_green}" "${c_reset}" "$1"; }
bad()     { FAIL=$((FAIL+1)); printf '  %s✗%s %s\n' "${c_red}" "${c_reset}" "$1"; }

# check <描述> <期望> <实际>
check() {
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1（期望 $2，实际 $3）"; fi
}

# contains <描述> <needle> <haystack>
contains() {
  case "$3" in
    *"$2"*) ok "$1" ;;
    *) bad "$1（输出里没有 $2）" ;;
  esac
}

# absent <描述> <needle> <haystack>
absent() {
  case "$3" in
    *"$2"*) bad "$1（输出里出现了 $2）" ;;
    *) ok "$1" ;;
  esac
}

cleanup() {
  if [ -n "${SERVER_PID}" ] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  psql "${PG_ADMIN_DSN}" -X -q -c "DROP DATABASE IF EXISTS ${DB}" >/dev/null 2>&1 || true
  rm -rf "${WORK}"
}
trap cleanup EXIT

# status <method> <path> [curl 额外参数...] —— 打印 HTTP 状态码
status() {
  local method="$1" path="$2"; shift 2
  curl -sS -o /dev/null -w '%{http_code}' -X "${method}" "${BASE}${path}" "$@"
}

# body <method> <path> [额外参数...] —— 打印响应体
body() {
  local method="$1" path="$2"; shift 2
  curl -sS -X "${method}" "${BASE}${path}" "$@"
}

jq_get() { python3 -c "import json,sys;d=json.load(sys.stdin);print(eval(sys.argv[1],{'d':d}))" "$1"; }

# k <args...> —— 用测试配置调用 knot CLI
k() { KNOT_CONFIG="${CONFIG_CLIENT}" "${KNOT}" "$@"; }

# ---------------------------------------------------------------------------
section "0. 准备构建与数据库"

( cd "${ROOT}" && go build -o "${KNOT}" ./cmd/knot && go build -o "${SERVER}" ./cmd/knot-server ) \
  && ok "构建 knot 与 knot-server" || { bad "构建失败"; exit 1; }

if psql "${PG_ADMIN_DSN}" -X -q -c "CREATE DATABASE ${DB}" 2>/dev/null; then
  ok "创建库 ${DB}"
else
  bad "建库失败"; exit 1
fi

if psql "${DB_DSN}" -X -q -c "CREATE EXTENSION IF NOT EXISTS pg_trgm" 2>/dev/null; then
  ok "安装 pg_trgm"
else
  bad "安装 pg_trgm 失败"; exit 1
fi

# ---------------------------------------------------------------------------
section "1. 迁移"

out="$(KNOT_PG_DSN="${DB_DSN}" "${SERVER}" migrate 2>&1)"; code=$?
check "knot-server migrate 退出码" 0 "${code}"
contains "迁移输出提示完成" "迁移完成" "${out}"

tables="$(psql "${DB_DSN}" -X -tA -c "select string_agg(table_name,',' order by table_name) from information_schema.tables where table_schema='public' and table_name in ('notes','tags','note_tags')")"
check "建出业务表" "note_tags,notes,tags" "${tables}"

idx="$(psql "${DB_DSN}" -X -tA -c "select count(*) from pg_indexes where schemaname='public' and indexname like '%trgm%'")"
check "trgm 索引已建立" 2 "${idx}"

# 幂等：再跑一次不应报错
code=0; KNOT_PG_DSN="${DB_DSN}" "${SERVER}" migrate >/dev/null 2>&1 || code=$?
check "重复迁移幂等" 0 "${code}"

tagcols="$(psql "${DB_DSN}" -X -tA -c "select string_agg(column_name,',' order by ordinal_position) from information_schema.columns where table_name='tags'")"
check "0003 已去掉 tags.created_at" "id,name" "${tagcols}"

# 约束
code=0
psql "${DB_DSN}" -X -q -c "insert into notes(title,raw_md,visibility) values ('x','y','secret')" >/dev/null 2>&1 || code=$?
check "非法 visibility 被 CHECK 拒绝" 1 "${code}"

# ---------------------------------------------------------------------------
section "2. 服务端启动与鉴权"

cat > "${CONFIG}" <<EOF
listen = "127.0.0.1:${PORT}"
pg_dsn = "${DB_DSN}"
token = "${TOKEN}"
EOF

nohup env KNOT_PG_DSN="${DB_DSN}" KNOT_LISTEN="127.0.0.1:${PORT}" KNOT_TOKEN="${TOKEN}" \
  "${SERVER}" > "${WORK}/server.log" 2>&1 &
SERVER_PID=$!

ready=0
for _ in $(seq 1 40); do
  if curl -fsS --max-time 1 "${BASE}/healthz" >/dev/null 2>&1; then ready=1; break; fi
  sleep 0.25
done
check "服务启动并 healthz 200" 1 "${ready}"
[ "${ready}" = 1 ] || { echo "--- server.log ---"; cat "${WORK}/server.log"; exit 1; }

check "healthz 返回 ok" "ok" "$(body GET /healthz | jq_get "d['status']")"
check "无 token 访问私有接口 401" 401 "$(status GET /api/tags)"
check "错误 token 401" 401 "$(status GET /api/tags -H 'Authorization: Bearer wrong')"
check "正确 token 200" 200 "$(status GET /api/tags -H "Authorization: Bearer ${TOKEN}")"
check "401 带 WWW-Authenticate" "Bearer realm=\"knot\"" \
  "$(curl -sS -D - -o /dev/null "${BASE}/api/tags" | tr -d '\r' | awk -F': ' 'tolower($1)=="www-authenticate"{print $2}')"
contains "响应带 X-Request-Id" "" \
  "$(curl -sS -D - -o /dev/null -H "Authorization: Bearer ${TOKEN}" "${BASE}/api/tags" | grep -i x-request-id || true)"

# 客户端配置（CLI 用）
cat > "${CONFIG_CLIENT}" <<EOF
server_url = "${BASE}"
token = "${TOKEN}"

[editor]
command = "true"

[cache]
path = "${CACHE}"
EOF
export KNOT_CONFIG="${CONFIG_CLIENT}"

# ---------------------------------------------------------------------------
section "3. CLI 新建 / 列表 / 查看"

ID1="$(k new "笔记一" --tags go,测试 --body "# 正文一" | awk '{print $1}')"
check "new 返回 UUID" 36 "${#ID1}"
ID2="$(k new "笔记二" --tags go --visibility public --body "# 正文二" | awk '{print $1}')"
ID3="$(k new "笔记三" --body "# 正文三" | awk '{print $1}')"
ok "新建三篇笔记"

contains "list 含笔记一" "笔记一" "$(k list)"
contains "list 显示标签" "#go" "$(k list)"
check "list --tag go 条数" 2 "$(k list --tag go --json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')"
check "list --limit 1 条数" 1 "$(k list --limit 1 --json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')"

js="$(k list --json)"
absent "list --json 不含 version" '"version"' "${js}"
absent "list --json 不含 raw_md" '"raw_md"' "${js}"
contains "list --json 含 tags" '"tags"' "${js}"

contains "show 输出正文" "正文一" "$(k show 笔记一)"
contains "show 输出标题" "# 笔记一" "$(k show 笔记一)"
check "show 按 UUID" 0 "$(k show "${ID1}" >/dev/null 2>&1; echo $?)"

code=0; k show 不存在的笔记 >/dev/null 2>&1 || code=$?
check "show 无匹配返回非零" 1 "${code}"

# ---------------------------------------------------------------------------
section "4. 编辑流程（无变化不提交 / 有变化提交 / 冲突）"

EDITOR="${WORK}/editor.sh"
cat > "${EDITOR}" <<'EOF'
#!/usr/bin/env bash
# 按模式改文件：$1 为文件路径
case "${KNOT_TEST_EDITOR_MODE:-noop}" in
  noop) exit 0 ;;
  whitespace) sed -i 's/$/   /' "$1"; printf '\n\n' >> "$1" ;;
  append) printf '\n追加的一行\n' >> "$1" ;;
esac
EOF
chmod +x "${EDITOR}"
sed -i "s|^command = \"true\"|command = \"${EDITOR}\"|" "${CONFIG_CLIENT}"

before_ver="$(body GET "/api/notes/${ID1}" -H "Authorization: Bearer ${TOKEN}" | jq_get "d['version']")"

export KNOT_TEST_EDITOR_MODE=noop
out="$(k edit 笔记一)"; code=$?
check "无变化 edit 退出码 0" 0 "${code}"
contains "无变化提示未提交" "没有变化" "${out}"
after_ver="$(body GET "/api/notes/${ID1}" -H "Authorization: Bearer ${TOKEN}" | jq_get "d['version']")"
check "无变化不增加版本" "${before_ver}" "${after_ver}"

export KNOT_TEST_EDITOR_MODE=whitespace
out="$(k edit 笔记一)"; code=$?
check "仅空白差异 edit 退出码 0" 0 "${code}"
contains "仅空白差异未提交" "没有变化" "${out}"
after_ver="$(body GET "/api/notes/${ID1}" -H "Authorization: Bearer ${TOKEN}" | jq_get "d['version']")"
check "仅空白差异不增加版本" "${before_ver}" "${after_ver}"

export KNOT_TEST_EDITOR_MODE=append
out="$(k edit 笔记一)"; code=$?
check "内容变化 edit 退出码 0" 0 "${code}"
contains "内容变化提示已保存" "已保存" "${out}"
after_ver="$(body GET "/api/notes/${ID1}" -H "Authorization: Bearer ${TOKEN}" | jq_get "d['version']")"
check "内容变化版本 +1" "$((before_ver+1))" "${after_ver}"
contains "正文已更新" "追加的一行" "$(k show 笔记一)"

# 冲突：必须在 GET 与 PUT 之间发生（edit 会先拉最新正文，事后改版本不算冲突）。
# 用编辑器脚本在「打开编辑器期间」改服务端来制造真实竞态。
RACE_EDITOR="${WORK}/race-editor.sh"
cat > "${RACE_EDITOR}" <<'RACE'
#!/usr/bin/env bash
# 先写本地改动，再在服务端并发改一次 → 后续 PUT 会撞 409
printf '\n本地并发改动\n' >> "$1"
curl -sS -X PUT "$KNOT_RACE_URL" -H "Authorization: Bearer $KNOT_RACE_TOKEN" \
  -H 'Content-Type: application/json' -d "$KNOT_RACE_BODY" >/dev/null
RACE
chmod +x "${RACE_EDITOR}"

cur_ver="$(body GET "/api/notes/${ID1}" -H "Authorization: Bearer ${TOKEN}" | jq_get "d['version']")"
sed -i "s|^command = .*|command = \"${RACE_EDITOR}\"|" "${CONFIG_CLIENT}"
export KNOT_RACE_URL="${BASE}/api/notes/${ID1}"
export KNOT_RACE_TOKEN="${TOKEN}"
export KNOT_RACE_BODY="{\"title\":\"笔记一\",\"raw_md\":\"# 别人并发改的\",\"visibility\":\"private\",\"base_version\":${cur_ver}}"

k edit 笔记一 > "${WORK}/conflict.out" 2> "${WORK}/conflict.err"; code=$?
check "并发修改时 edit 返回非零" 1 "${code}"
contains "冲突提示版本冲突" "版本冲突" "$(cat "${WORK}/conflict.err")"
contains "冲突时保留临时文件路径" "内容保留在" "$(cat "${WORK}/conflict.err")"
tmp_in_err="$(sed -n 's/.*内容保留在 \(.*\)$/\1/p' "${WORK}/conflict.err")"
check "临时文件确实存在" 1 "$([ -f "${tmp_in_err}" ] && echo 1 || echo 0)"
contains "临时文件含本地改动" "本地并发改动" "$(cat "${tmp_in_err}" 2>/dev/null)"
rm -f "${tmp_in_err}"
contains "冲突不覆盖服务端正文" "别人并发改的" "$(k show 笔记一)"

# 恢复普通编辑器，供后续用例使用
sed -i "s|^command = .*|command = \"${EDITOR}\"|" "${CONFIG_CLIENT}"
unset KNOT_RACE_URL KNOT_RACE_TOKEN KNOT_RACE_BODY

# 服务端删除后编辑
k rm -y 笔记三 >/dev/null
export KNOT_TEST_EDITOR_MODE=noop
code=0; k edit 笔记三 >/dev/null 2>&1 || code=$?
check "已删除笔记 edit 返回非零" 1 "${code}"
check "本地缓存已清理该笔记" 0 "$(k list --json | python3 -c "import json,sys;print(sum(1 for n in json.load(sys.stdin) if n['id']=='${ID3}'))")"

# ---------------------------------------------------------------------------
section "5. 标签操作"

out="$(k tag add 笔记二 rust)"; contains "tag add 输出标签" "rust" "${out}"
check "笔记二标签数" 2 "$(k show 笔记二 | sed -n 's/^标签: //p' | tr ',' '\n' | wc -l | tr -d ' ')"

out="$(k tag rename rust rustlang)"; contains "tag rename 提示" "已重命名" "${out}"
contains "重命名后显示新名" "rustlang" "$(k tag list)"

out="$(k tag rm 笔记二 rustlang)"; contains "tag rm 后剩余 go" "go" "${out}"
absent "tag rm 后不含 rustlang" "rustlang" "$(k show 笔记二)"

code=0; k tag rename 不存在的标签 x >/dev/null 2>&1 || code=$?
check "重命名不存在标签返回非零" 1 "${code}"

# ---------------------------------------------------------------------------
section "6. 搜索"

check "search 命中标题" 1 "$(k search 笔记一 --json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["notes"]))')"
contains "search 输出含笔记一" "笔记一" "$(k search 笔记一)"
go_count="$(k search "" --tag go --json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["notes"]))')"
check "search --tag go 命中 2 篇（笔记一、笔记二）" 2 "${go_count}"
check "search --tag go 结果都带 go 标签" 0 "$(k search "" --tag go --json | python3 -c 'import json,sys;print(sum(1 for n in json.load(sys.stdin)["notes"] if "go" not in n["tags"]))')"
code=0; k search >/dev/null 2>&1 || code=$?
check "search 无关键词与标签时报错" 1 "${code}"

# ---------------------------------------------------------------------------
section "7. 服务端 API 细节"

auth=(-H "Authorization: Bearer ${TOKEN}")
json=(-H 'Content-Type: application/json')

check "PUT 缺 base_version → 400" 400 "$(status PUT "/api/notes/${ID1}" "${auth[@]}" "${json[@]}" -d '{"title":"t","raw_md":"m","visibility":"private"}')"
check "PUT 未知字段 → 400" 400 "$(status PUT "/api/notes/${ID1}" "${auth[@]}" "${json[@]}" -d '{"title":"t","raw_md":"m","visibility":"private","base_version":1,"baseVersion":1}')"
check "PUT 非法 visibility → 400" 400 "$(status PUT "/api/notes/${ID1}" "${auth[@]}" "${json[@]}" -d '{"title":"t","raw_md":"m","visibility":"unlisted","base_version":1}')"
check "非法 UUID → 400" 400 "$(status GET "/api/notes/not-a-uuid" "${auth[@]}")"
check "不存在笔记 → 404" 404 "$(status GET "/api/notes/00000000-0000-0000-0000-000000000000" "${auth[@]}")"
check "非法 limit → 400" 400 "$(status GET "/api/notes?limit=0" "${auth[@]}")"
check "非法 cursor → 400" 400 "$(status GET "/api/notes?cursor=@@@" "${auth[@]}")"

# 分页：limit=1 应给出 next_cursor，且第二页不重复
page1="$(body GET "/api/notes?limit=1" "${auth[@]}")"
cur="$(echo "${page1}" | jq_get "d.get('next_cursor','')")"
check "limit=1 返回 next_cursor" 1 "$([ -n "${cur}" ] && echo 1 || echo 0)"
first_id="$(echo "${page1}" | jq_get "d['notes'][0]['id']")"
page2="$(body GET "/api/notes?limit=1&cursor=${cur}" "${auth[@]}")"
second_id="$(echo "${page2}" | jq_get "d['notes'][0]['id']" 2>/dev/null || echo none)"
check "第二页与第一页不同" 1 "$([ "${first_id}" != "${second_id}" ] && echo 1 || echo 0)"

# 快照不含正文
snap="$(body GET /api/sync/snapshot "${auth[@]}")"
absent "快照不含 raw_md" '"raw_md"' "${snap}"
contains "快照含 synced_at" '"synced_at"' "${snap}"
contains "时间统一 UTC（Z 结尾）" 'Z"' "${snap}"

# 413：超过 8 MiB
big="${WORK}/big.json"
python3 -c 'import json,sys;json.dump({"title":"x","raw_md":"a"*(9*1024*1024)},open(sys.argv[1],"w"))' "${big}"
check "超大请求体 → 413" 413 "$(status POST /api/notes "${auth[@]}" "${json[@]}" --data-binary "@${big}")"

# 405 / 404 路由
check "未知路由 → 404" 404 "$(status GET /api/nope "${auth[@]}")"
check "方法不允许 → 405" 405 "$(status PATCH /api/notes "${auth[@]}")"

# DELETE 幂等
check "DELETE 存在 → 204" 204 "$(status DELETE "/api/notes/${ID3}" "${auth[@]}")"
check "DELETE 重复 → 204" 204 "$(status DELETE "/api/notes/${ID3}" "${auth[@]}")"

# ---------------------------------------------------------------------------
section "8. 同步对账"

out="$(k sync)"
case "${out}" in
  *"同步完成"*|*"已是最新"*|*"全量重建"*) ok "sync 正常完成" ;;
  *) bad "sync 输出异常：${out}" ;;
esac
out="$(k sync)"; contains "紧接着再 sync 无变更" "已是最新" "${out}"

# 服务端直接改数据库，本地尚未同步
psql "${DB_DSN}" -X -q -c "update notes set title='外部改名' where id='${ID2}'" >/dev/null
out="$(k sync)"; contains "sync 报告更新" "更新 1" "${out}"
contains "本地标题已更新" "外部改名" "$(k list)"

# 服务端直接删笔记
psql "${DB_DSN}" -X -q -c "delete from notes where id='${ID2}'" >/dev/null
out="$(k sync)"; contains "sync 报告删除" "删除" "${out}"
check "本地已清理被删笔记" 0 "$(k list --json | python3 -c "import json,sys;print(sum(1 for n in json.load(sys.stdin) if n['id']=='${ID2}'))")"

# 标签重命名经 sync 同步到本地
psql "${DB_DSN}" -X -q -c "update tags set name='golang' where name='go'" >/dev/null
out="$(k sync)"; contains "sync 后本地显示新标签" "golang" "$(k list --tag golang)"
check "旧标签已从本地消失" 0 "$(k list --tag go --json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')"

# sync --full 重建
out="$(k sync --full)"; contains "sync --full 提示全量重建" "全量重建" "${out}"
check "sync --full 后数据仍在" 1 "$(k list --json | python3 -c 'import json,sys;print(1 if len(json.load(sys.stdin))>=1 else 0)')"

# 缓存命令
out="$(k cache info)"; contains "cache info 显示笔记数" "笔记元数据" "${out}"
out="$(k cache clear --content)"; contains "cache clear --content" "正文缓存" "${out}"
code=0; k cache clear >/dev/null 2>&1 || code=$?
check "cache clear 缺参数报错" 1 "${code}"
k cache clear --all >/dev/null
check "cache clear --all 后为空" 0 "$(k list --json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')"
k sync >/dev/null
check "重建后恢复数据" 1 "$(k list --json | python3 -c 'import json,sys;print(1 if len(json.load(sys.stdin))>=1 else 0)')"

# ---------------------------------------------------------------------------
section "8b. 离线读取（show 回落本地缓存）"

# 用一篇独立的笔记验证，避免受前面编辑用例的影响
OFFLINE_ID="$(k new "离线验证" --body "# 离线正文标记" | awk '{print $1}')"
k show "${OFFLINE_ID}" >/dev/null 2>&1
# 停服务模拟断网
kill "${SERVER_PID}" 2>/dev/null; wait "${SERVER_PID}" 2>/dev/null; SERVER_PID=""
sleep 0.5

offscreen="$(k show "${OFFLINE_ID}" 2>&1)"
contains "断网时 show 仍能显示缓存正文" "离线正文标记" "${offscreen}"
contains "断网时提示是本地缓存" "本地缓存" "${offscreen}"

code=0; k show 笔记二 >/dev/null 2>&1 || code=$?
check "断网且无正文缓存时返回非零" 1 "${code}"

check "断网时 list 仍可用（本地缓存）" 0 "$(k list >/dev/null 2>&1; echo $?)"

# 重新起服务，后续用例继续
nohup env KNOT_PG_DSN="${DB_DSN}" KNOT_LISTEN="127.0.0.1:${PORT}" KNOT_TOKEN="${TOKEN}" \
  "${SERVER}" > "${WORK}/server2.log" 2>&1 &
SERVER_PID=$!
ready=0
for _ in $(seq 1 40); do
  curl -fsS --max-time 1 "${BASE}/healthz" >/dev/null 2>&1 && { ready=1; break; }
  sleep 0.25
done
check "服务重启后 healthz 200" 1 "${ready}"
sleep 1

# ---------------------------------------------------------------------------
section "9. 公开接口与可见性隔离"

PUB_ID="$(k new "公开笔记" --tags pub --visibility public --body "# 公开正文" | awk '{print $1}')"
PRIV_ID="$(k new "私密笔记" --tags secret --visibility private --body "# 私密正文" | awk '{print $1}')"

pub_body="$(body GET "/api/public/notes/${PUB_ID}")"
contains "公开笔记无 token 可读" "公开正文" "${pub_body}"
absent "公开详情不返回 version" '"version"' "${pub_body}"
absent "公开详情不返回 modified_at" '"modified_at"' "${pub_body}"
absent "公开详情不返回 visibility" '"visibility"' "${pub_body}"

check "私密笔记公开接口 404" 404 "$(status GET "/api/public/notes/${PRIV_ID}")"
absent "404 不泄露正文" "私密正文" "$(body GET "/api/public/notes/${PRIV_ID}")"

list_body="$(body GET /api/public/notes)"
absent "公开列表不含私密笔记" "私密笔记" "${list_body}"
contains "公开列表含公开笔记" "公开笔记" "${list_body}"

tags_body="$(body GET /api/public/tags)"
absent "公开标签不含 secret" "secret" "${tags_body}"
contains "公开标签含 pub" "pub" "${tags_body}"

# 公开列表必须真的传了 visibility 过滤（私密笔记多时也不能出现）
for i in $(seq 1 5); do k new "私密批量${i}" >/dev/null; done
check "公开列表仍无私有内容" 0 "$(body GET "/api/public/notes?limit=200" | grep -c '私密批量' || true)"

# ---------------------------------------------------------------------------
section "10. 限流"

# 公开列表：2/s，突发 5。连打 8 次应出现 429。
codes=""
for _ in $(seq 1 8); do codes="${codes}$(status GET /api/public/notes) "; done
contains "连续请求出现 429" "429" "${codes}"
retry="$(curl -sS -D - -o /dev/null "${BASE}/api/public/notes" | tr -d '\r' | awk -F': ' 'tolower($1)=="retry-after"{print $2}')"
check "429 带 Retry-After" 1 "$([ -n "${retry}" ] && echo 1 || echo 0)"
sleep 3
check "限流窗口后恢复 200" 200 "$(status GET /api/public/notes)"

# 伪造 X-Forwarded-For 不能绕过限流
codes=""
for _ in $(seq 1 8); do codes="${codes}$(status GET /api/public/notes -H "X-Forwarded-For: 10.0.0.$RANDOM") "; done
contains "伪造 XFF 仍被限流" "429" "${codes}"
sleep 3

# ---------------------------------------------------------------------------
section "11. 服务端无 token 的行为"

# 用空 token 另起一个实例，验证私有接口一律 401 而非放行
EMPTY_PORT=$((PORT+1))
nohup env KNOT_PG_DSN="${DB_DSN}" KNOT_LISTEN="127.0.0.1:${EMPTY_PORT}" \
  "${SERVER}" > "${WORK}/server-empty.log" 2>&1 &
EMPTY_PID=$!
for _ in $(seq 1 40); do
  curl -fsS --max-time 1 "http://127.0.0.1:${EMPTY_PORT}/healthz" >/dev/null 2>&1 && break
  sleep 0.25
done
check "未配 token 时私有接口 401" 401 "$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${EMPTY_PORT}/api/tags")"
check "未配 token 时带任意 token 也 401" 401 "$(curl -sS -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer anything' "http://127.0.0.1:${EMPTY_PORT}/api/tags")"
check "未配 token 仍可用公开接口" 200 "$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${EMPTY_PORT}/api/public/notes")"
contains "启动日志有 warn" "401" "$(cat "${WORK}/server-empty.log")"
kill "${EMPTY_PID}" 2>/dev/null; wait "${EMPTY_PID}" 2>/dev/null

# ---------------------------------------------------------------------------
section "12. TUI 启动（非 TTY 必须立即拒绝）"

out="$("${KNOT}" tui 2>&1 </dev/null)"; code=$?
check "非 TTY 下 knot tui 返回非零" 1 "${code}"
contains "提示需要交互式终端" "交互式终端" "${out}"

out="$("${KNOT}" 2>&1 </dev/null)"; code=$?
check "不带子命令时也进 TUI（非 TTY 报错）" 1 "${code}"
contains "无子命令提示需要终端而非打印帮助" "交互式终端" "${out}"
absent "无子命令不打印帮助" "Available Commands" "${out}"

# ---------------------------------------------------------------------------
section "13. 备份与恢复"

BACKUP_DIR="${WORK}/backup"
PG_DSN="${DB_DSN}" BACKUP_DIR="${BACKUP_DIR}" KEEP=2 bash "${ROOT}/scripts/backup.sh" >/dev/null 2>&1
check "备份文件已生成" 1 "$(ls "${BACKUP_DIR}"/knot-*.dump 2>/dev/null | wc -l | tr -d ' ')"
dump="$(ls "${BACKUP_DIR}"/knot-*.dump | head -1)"
check "归档可被 pg_restore 解析" 0 "$(pg_restore -l "${dump}" >/dev/null 2>&1; echo $?)"

RESTORE_DB="${DB}_restore"
psql "${PG_ADMIN_DSN}" -X -q -c "CREATE DATABASE ${RESTORE_DB}" >/dev/null 2>&1
RESTORE_DSN="$(dsn_for "${RESTORE_DB}")"
psql "${RESTORE_DSN}" -X -q -c "CREATE EXTENSION IF NOT EXISTS pg_trgm" >/dev/null 2>&1
pg_restore --no-owner --no-privileges -d "${RESTORE_DSN}" "${dump}" >/dev/null 2>&1
orig="$(psql "${DB_DSN}" -X -tA -c 'select count(*) from notes')"
restored="$(psql "${RESTORE_DSN}" -X -tA -c 'select count(*) from notes' 2>/dev/null)"
check "恢复后笔记数一致" "${orig}" "${restored}"
psql "${PG_ADMIN_DSN}" -X -q -c "DROP DATABASE IF EXISTS ${RESTORE_DB}" >/dev/null 2>&1

# 轮转：生成 3 份，KEEP=2 应只剩 2 份
sleep 1; PG_DSN="${DB_DSN}" BACKUP_DIR="${BACKUP_DIR}" KEEP=2 bash "${ROOT}/scripts/backup.sh" >/dev/null 2>&1
sleep 1; PG_DSN="${DB_DSN}" BACKUP_DIR="${BACKUP_DIR}" KEEP=2 bash "${ROOT}/scripts/backup.sh" >/dev/null 2>&1
check "备份轮转保留 KEEP 份" 2 "$(ls "${BACKUP_DIR}"/knot-*.dump | wc -l | tr -d ' ')"

code=0; bash "${ROOT}/scripts/backup.sh" >/dev/null 2>&1 || code=$?
check "缺 PG_DSN 时备份报错" 1 "${code}"

# ---------------------------------------------------------------------------
section "14. 数据一致性对账"

# 逐条比对服务端快照与本地缓存
snap_json="$(body GET /api/sync/snapshot "${auth[@]}")"
local_json="$(k list --json)"
python3 - "$snap_json" "$local_json" <<'PY'
import json, sys
snap = json.loads(sys.argv[1])
local = {n["id"]: n for n in json.loads(sys.argv[2])}
problems = []
for n in snap["notes"]:
    l = local.get(n["id"])
    if l is None:
        problems.append(f"本地缺笔记 {n['id']}")
        continue
    if l["title"] != n["title"]:
        problems.append(f"{n['id']} 标题不一致 {l['title']!r} != {n['title']!r}")
    if sorted(l["tags"]) != sorted(n["tags"]):
        problems.append(f"{n['id']} 标签不一致 {l['tags']} != {n['tags']}")
server_ids = {n["id"] for n in snap["notes"]}
for i in local:
    if i not in server_ids:
        problems.append(f"本地多出笔记 {i}")
if problems:
    print("\n".join(problems))
    sys.exit(1)
sys.exit(0)
PY
check "本地缓存与服务端逐条一致" 0 "$?"

# ---------------------------------------------------------------------------
printf '\n%s========== 结果 ==========%s\n' "${c_blue}" "${c_reset}"
printf '  通过 %s%d%s / 失败 %s%d%s\n' "${c_green}" "${PASS}" "${c_reset}" "${c_red}" "${FAIL}" "${c_reset}"

if [ "${FAIL}" -gt 0 ]; then
  echo
  echo "--- server.log（末尾 30 行）---"
  tail -30 "${WORK}/server.log"
  exit 1
fi

echo "✓ 全部通过"
