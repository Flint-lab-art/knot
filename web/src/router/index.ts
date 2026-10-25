import { createRouter, createWebHistory } from 'vue-router'

import NoteDetail from '@/views/NoteDetail.vue'
import NoteList from '@/views/NoteList.vue'
import NotFound from '@/views/NotFound.vue'

// 路由与 docs/WEB.md 一致：/、/notes/:id、兜底 404。
// 用 createWebHistory，生产由 Caddy 的 SPA fallback 支持。
const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', name: 'home', component: NoteList },
    { path: '/notes/:id', name: 'note', component: NoteDetail },
    { path: '/:pathMatch(.*)*', name: 'notfound', component: NotFound },
  ],
  scrollBehavior(_to, _from, saved) {
    return saved ?? { top: 0 }
  },
})

export default router
