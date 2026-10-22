<script setup>
defineProps({
  size: { type: [Number, String], default: 18 },
})
</script>

<template>
  <svg
    xmlns="http://www.w3.org/2000/svg"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M3.5 5.4c2.8-.7 5.5-.2 8.5 1.5v12c-3-1.7-5.7-2.2-8.5-1.5V5.4Z" />
    <path d="M20.5 5.4c-2.8-.7-5.5-.2-8.5 1.5v12c3-1.7 5.7-2.2 8.5-1.5V5.4Z" />
    <path d="M6 8.2h3.1v3H6zM6 13.2h3.1v2.9H6z" />
    <path d="M15 8.2h3v2.6h-1.3l-.8.8v-.8H15z" />
    <path d="M14.9 13.2h3.2v2.9h-3.2z" />
  </svg>
</template>
