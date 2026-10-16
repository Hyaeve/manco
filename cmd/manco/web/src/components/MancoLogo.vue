<script setup>
defineProps({
  size: { type: [Number, String], default: 36 },
  title: { type: String, default: 'Manco' },
})
</script>

<template>
  <img
    class="manco-logo"
    src="/logo-icon.png"
    :width="size"
    :height="size"
    :alt="title"
    draggable="false"
  />
</template>

<style scoped>
.manco-logo {
  display: block;
  object-fit: contain;
  user-select: none;
}
</style>
