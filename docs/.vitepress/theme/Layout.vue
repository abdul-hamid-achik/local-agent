<script setup lang="ts">
import DefaultTheme from 'vitepress/theme-without-fonts'
import { useData } from 'vitepress'
import { computed } from 'vue'
import Home from './Home.vue'

const { Layout: DefaultLayout } = DefaultTheme
const { frontmatter } = useData()
const isHome = computed(() => frontmatter.value.layout === 'home')
</script>

<template>
  <DefaultLayout>
    <template #home-hero-before>
      <aside v-if="!frontmatter.currentImplementation" class="migration-notice" aria-label="Documentation version">
        This site documents the historical Go releases. Current development uses
        Bun and Vue; feature parity is still in progress.
        <a href="https://github.com/abdul-hamid-achik/local-agent#development-and-usage">Try the experimental client</a>.
      </aside>
      <Home v-if="isHome" />
    </template>
    <template #doc-before>
      <aside v-if="!frontmatter.currentImplementation" class="migration-notice" aria-label="Documentation version">
        This page describes the historical Go releases. Current development uses
        Bun and Vue; feature parity is still in progress.
        <a href="https://github.com/abdul-hamid-achik/local-agent#development-and-usage">Experimental client instructions</a>.
      </aside>
    </template>
  </DefaultLayout>
</template>

<style scoped>
.migration-notice {
  margin: 24px auto;
  padding: 16px 24px;
  max-width: 1152px;
  color: var(--vp-c-text-1);
  background: var(--vp-c-bg-soft);
  font-size: 14px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.migration-notice a { color: var(--vp-c-brand-1); text-decoration: underline; }
</style>
