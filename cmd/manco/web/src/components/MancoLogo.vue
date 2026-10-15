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
      class="logo-panel"
      d="M15.5 11.5h33a6 6 0 0 1 6 6v29a6 6 0 0 1-6 6h-33a6 6 0 0 1-6-6v-29a6 6 0 0 1 6-6Z"
    />
    <path
      class="logo-bookmark"
      d="M40.2 11.5v13.4l5.5-4.6 5.5 4.6V11.5"
    />
    <circle class="logo-dot" cx="37.5" cy="46.5" r="4" />
  </svg>
</template>

<style scoped>
.logo-panel,
.logo-bookmark {
  stroke-linecap: round;
  stroke-linejoin: round;
}

.logo-panel,
.logo-bookmark {
  stroke: currentColor;
}

.logo-panel {
  stroke-width: 3.2;
}

.logo-bookmark {
  stroke-width: 2.8;
}

.logo-dot {
  fill: var(--logo-accent, #e0a338);
}
</style>
