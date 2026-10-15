<script setup>
// 简洁线条的返回箭头：横线比 lucide ArrowLeft 略长，箭头帽略短，线条更细。
defineProps({
  size: { type: Number, default: 25 },
  strokeWidth: { type: Number, default: 1.5 },
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
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M3 12h17" />
    <path d="M9 6l-6 6 6 6" />
  </svg>
</template>
