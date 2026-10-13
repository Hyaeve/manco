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
      d="M32 18c-6-4.4-12.2-5.4-20-4.2v32c7.8-1.2 14-.2 20 4.2 6-4.4 12.2-5.4 20-4.2v-32C44.2 12.6 38 13.6 32 18Z"
      stroke="#C9982A"
      stroke-width="3"
      stroke-linejoin="round"
    />
    <path d="M32 18v32" stroke="#C9982A" stroke-width="3" stroke-linecap="round" />
    <path
      d="M31.8 27.6v11.2m-5.4-5.4 5.4 5.4 5.4-5.4M26.4 43.8h11"
      stroke="#6F7F4A"
      stroke-width="2.7"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <path
      d="M20 20.6c3.4-.1 6.6.5 9.4 1.8M20 26.4c3.4-.1 6.6.5 9.4 1.8M44 20.6c-3.4-.1-6.6.5-9.4 1.8M44 26.4c-3.4-.1-6.6.5-9.4 1.8"
      stroke="#C9982A"
      stroke-width="2.2"
      stroke-linecap="round"
      opacity="0.55"
    />
  </svg>
</template>
