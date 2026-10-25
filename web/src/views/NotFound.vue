<script setup lang="ts">
// 404：一句提示 + 回列表，不做花哨插画（docs/WEB.md）。
</script>

<template>
  <section class="notfound">
    <h1 class="notfound__title">页面不存在</h1>
    <RouterLink class="notfound__back" :to="{ name: 'home' }">回列表</RouterLink>
  </section>
</template>

<style scoped>
.notfound {
  margin: 96px auto;
  max-width: var(--content-width);
  text-align: center;
}

.notfound__title {
  margin: 0 0 16px;
  font-size: var(--font-size-h2);
  font-weight: 600;
}

.notfound__back {
  display: inline-block;
  padding: 7px 18px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  font-size: var(--font-size-meta);
  transition: border-color var(--duration) var(--ease);
}

.notfound__back:hover {
  text-decoration: none;
  border-color: var(--color-accent);
}
</style>
