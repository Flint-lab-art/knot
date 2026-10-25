<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <header class="topbar">
    <div class="topbar__inner">
      <RouterLink class="topbar__brand" :to="{ name: 'home' }">Knot</RouterLink>
    </div>
  </header>

  <main class="page">
    <RouterView />
  </main>
</template>

<style scoped>
.topbar {
  border-bottom: 1px solid var(--color-border);
}

.topbar__inner,
.page {
  max-width: var(--content-width);
  margin: 0 auto;
  padding: 0 var(--pad-x);
}

.topbar__brand {
  display: inline-block;
  padding: 16px 0;
  color: var(--color-text);
  font-size: 20px;
  font-weight: 700;
  letter-spacing: -0.01em;
}

.topbar__brand:hover {
  text-decoration: none;
  color: var(--color-accent);
}

.page {
  padding-top: 8px;
  padding-bottom: 32px;
}
</style>
