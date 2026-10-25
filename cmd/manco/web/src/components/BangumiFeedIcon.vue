<script setup>
defineProps({
  feed: { type: String, required: true },
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
    stroke-width="1.7"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <g v-if="feed === 'recent'">
      <path d="M10.2 3.4 11.5 6l2.6 1.2-2.6 1.2-1.3 2.6-1.2-2.6-2.7-1.2 2.7-1.2Z" />
      <circle cx="16.8" cy="16.4" r="4" />
      <path d="M16.8 14.4v2.2l1.5 1" />
    </g>
    <g v-else-if="feed === 'airing'">
      <circle cx="12" cy="12" r="7.6" />
      <path d="m10.2 8.4 5 3.6-5 3.6Z" />
      <path d="M2.7 9.2A9.5 9.5 0 0 1 5 5m16.3 4.2A9.5 9.5 0 0 0 19 5" />
    </g>
    <g v-else-if="feed === 'monthly'">
      <rect x="4" y="5.4" width="16" height="14.1" rx="2.2" />
      <path d="M4 9.3h16M8 3.5v3.7M16 3.5v3.7" />
      <path d="m15.2 13.1.7 1.4 1.6.2-1.2 1.1.3 1.6-1.4-.8-1.4.8.3-1.6-1.2-1.1 1.6-.2Z" />
    </g>
    <g v-else-if="feed === 'yearly'">
      <path d="M6.2 5.5h11.6A2.2 2.2 0 0 1 20 7.7v10.1a2.2 2.2 0 0 1-2.2 2.2H6.2A2.2 2.2 0 0 1 4 17.8V7.7a2.2 2.2 0 0 1 2.2-2.2Z" />
      <path d="M8 3.5v3.8M16 3.5v3.8M4 9.2h16" />
      <path d="M8.3 12.1h3.2M8.3 15.7h2.2M14 12.1h1.8M14 15.7h2" />
      <path d="m18.8 3.7.7 1.4 1.5.2-1.1 1.1.3 1.5-1.4-.7-1.3.7.3-1.5-1.1-1.1 1.5-.2Z" />
    </g>
    <g v-else-if="feed === 'popular'">
      <path d="M6 4h9.3A2.7 2.7 0 0 1 18 6.7V21l-6-3.4L6 21Z" />
      <path d="m17.3 2.8.7 1.5 1.6.2-1.2 1.1.3 1.6-1.4-.8-1.4.8.3-1.6-1.2-1.1 1.6-.2Z" />
    </g>
    <g v-else-if="feed === 'ranking'">
      <path d="M4.2 20v-5.3h4.4V20M9.8 20V9.1h4.4V20M15.4 20v-7.2h4.4V20M3 20h18" />
      <path d="m8.2 4 1-1.5 2.8 1 2.8-1 1 1.5-1.4 3H9.6Z" />
      <path d="M9.6 7h4.8" />
    </g>
    <g v-else-if="feed === 'manga'">
      <path d="M4 5.2h6.2v13.6H4Z" />
      <path d="M10.2 5.2H20v8.5h-5l-2.8 2.5v-2.5h-2Z" />
      <path d="M6.2 8.2h1.8M6.2 11h1.8M13 8.3h4M13 11h3" />
    </g>
    <g v-else-if="feed === 'novel'">
      <path d="M3.8 6.2A3.3 3.3 0 0 1 7.1 3h4.2v16.8H7.1a3.3 3.3 0 0 0-3.3 3.2Z" />
      <path d="M20.2 6.2A3.3 3.3 0 0 0 16.9 3h-4.2v16.8h4.2a3.3 3.3 0 0 1 3.3 3.2Z" />
      <path d="M16.2 7.1c-2.2.2-3.7 1.4-4.3 3.4" />
    </g>
    <g v-else>
      <path d="m12 3 1.4 3.6L17 8l-3.6 1.4L12 13l-1.4-3.6L7 8l3.6-1.4Z" />
      <path d="m18.2 14 .8 2.1 2.1.8-2.1.8-.8 2.1-.8-2.1-2.1-.8 2.1-.8Z" />
    </g>
  </svg>
</template>
