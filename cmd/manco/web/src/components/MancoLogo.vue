<script setup>
defineProps({
  size: { type: [Number, String], default: 36 },
  title: { type: String, default: 'Manco' },
})
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 64 64"
    role="img"
    :aria-label="title"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <path
      class="logo-page"
      d="M13.5 17.2c6.2-2.1 12.4-2.1 18.5 0v32.5c-6.1-2.1-12.3-2.1-18.5 0z"
    />
    <path
      class="logo-page"
      d="M50.5 17.2c-6.2-2.1-12.4-2.1-18.5 0v32.5c6.1-2.1 12.3-2.1 18.5 0z"
    />
    <path
      class="logo-arrow"
      d="M32 25.5v12.6m-5.2-5.2 5.2 5.2 5.2-5.2"
    />
  </svg>
</template>

<style scoped>
.logo-page,
.logo-arrow {
  stroke-linecap: round;
  stroke-linejoin: round;
}

.logo-page {
  stroke: currentColor;
  stroke-width: 3;
}

.logo-arrow {
  stroke: var(--logo-accent, #75864a);
  stroke-width: 2.8;
}
</style>
