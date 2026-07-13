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
      <Home v-if="isHome" />
    </template>
  </DefaultLayout>
</template>
