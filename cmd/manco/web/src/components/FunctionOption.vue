<script setup>
// 统一的功能开关：圆角框 + 勾选方框 + 文字，四列网格里复用。
import { Check } from 'lucide-vue-next'

defineProps({
  label: { type: String, required: true },
  active: { type: Boolean, default: false },
})
defineEmits(['toggle'])
</script>

<template>
  <button
    class="function-option"
    :class="{ active }"
    type="button"
    role="checkbox"
    :aria-checked="active"
    @click="$emit('toggle')"
  >
    <span class="function-option-check"><Check v-if="active" :size="13" :stroke-width="2.4" /></span>
    <span class="function-option-label">{{ label }}</span>
  </button>
</template>
