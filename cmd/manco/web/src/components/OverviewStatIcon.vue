<script setup>
defineProps({
  name: { type: String, required: true },
  size: { type: [Number, String], default: 30 },
  strokeWidth: { type: [Number, String], default: 1.7 },
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
    <g v-if="name === 'subscriptions'">
      <path d="M6.5 5.2h8.2A2.3 2.3 0 0 1 17 7.5V20l-5.25-2.8L6.5 20Z" />
      <path d="M9.2 5.2V3.7h7.2a3 3 0 0 1 3 3v7.6" />
      <circle cx="12.2" cy="10.7" r="2.1" />
      <path d="M12.2 9.5v1.5l1 .7" />
    </g>
    <g v-else-if="name === 'library'">
      <path d="M4 5.2h5.4v13.6H4Z" />
      <path d="M9.4 5.2h4.8v13.6H9.4Z" />
      <path d="m15.1 6.4 3.5-1 3 12.3-3.5 1Z" />
      <path d="M3 19.3h18" />
      <path d="M5.5 8.3h2.4M5.5 11h2.4M11 8.3h1.8M11 11h1.8" />
    </g>
    <g v-else-if="name === 'completed'">
      <circle cx="11.4" cy="12.2" r="7.4" />
      <path d="m7.9 12.2 2.3 2.3 4.7-5" />
      <path d="M19.5 4.4v3.1M18 6h3M20.6 9.3l1.4 1.4" />
    </g>
    <g v-else-if="name === 'failed'">
      <path d="M12 3.2 21 8v8L12 20.8 3 16V8Z" />
      <path d="M12 8v5.2" />
      <circle cx="12" cy="16.3" r=".7" fill="currentColor" stroke="none" />
    </g>
    <g v-else-if="name === 'running'">
      <path d="M12 3.8v9.4" />
      <path d="m8.4 9.8 3.6 3.6 3.6-3.6" />
      <path d="M4.2 14.5v3.7A2.2 2.2 0 0 0 6.4 20h11.2a2.2 2.2 0 0 0 2.2-2.2v-3.3" />
      <path d="M4.2 15.2h4l1.1 1.8h5.4l1.1-1.8h4" />
    </g>
    <g v-else-if="name === 'queued'">
      <circle cx="5" cy="6.2" r="1.6" />
      <circle cx="5" cy="12" r="1.6" />
      <circle cx="5" cy="17.8" r="1.6" />
      <path d="M9 6.2h10.5M9 12h8M9 17.8h9.5" />
    </g>
  </svg>
</template>
