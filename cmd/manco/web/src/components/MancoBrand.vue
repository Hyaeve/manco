<script setup>
defineProps({
  tagline: { type: String, default: '漫画书籍 · 订阅下载' },
})
</script>

<template>
  <span class="brand-mark">
    <img class="brand-lockup brand-lockup-light" src="/brand-lockup.png" alt="Manco 漫画书籍订阅下载" />
    <img class="brand-lockup brand-lockup-dark" src="/brand-lockup-plate.png" alt="Manco 漫画书籍订阅下载" />
    <small class="brand-tagline">{{ tagline }}</small>
  </span>
</template>
