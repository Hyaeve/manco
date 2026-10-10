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
    xmlns="http://www.w3.org/2000/svg"
  >
    <rect x="2" y="2" width="60" height="60" rx="14" fill="#312E81" />
    <path
      d="M12.5 18.5c6.4-2.4 12.2-2 19.5 2.1v28.2c-7.3-4.1-13.1-4.5-19.5-2.1V18.5Z"
      fill="#F8FAFC"
    />
    <path
      d="M51.5 18.5c-6.4-2.4-12.2-2-19.5 2.1v28.2c7.3-4.1 13.1-4.5 19.5-2.1V18.5Z"
      fill="#C7D2FE"
    />
    <path
      d="M32 23.5v15M25.5 32.5 32 39l6.5-6.5"
      fill="none"
      stroke="#FBBF24"
      stroke-width="4"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>
