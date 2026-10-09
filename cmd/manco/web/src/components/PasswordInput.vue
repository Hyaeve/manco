<script setup>
import { ref } from 'vue'
import { Eye, EyeOff } from 'lucide-vue-next'

defineProps({
  modelValue: { type: String, default: '' },
  autocomplete: { type: String, default: 'current-password' },
  placeholder: { type: String, default: '' },
})
defineEmits(['update:modelValue'])

const visible = ref(false)
</script>

<template>
  <div class="password-control">
    <input
      class="input"
      :type="visible ? 'text' : 'password'"
      :value="modelValue"
      :autocomplete="autocomplete"
      :placeholder="placeholder"
      @input="$emit('update:modelValue', $event.target.value)"
    />
    <button
      class="password-toggle"
      type="button"
      :aria-label="visible ? '隐藏密码' : '显示密码'"
      :title="visible ? '隐藏密码' : '显示密码'"
      @click="visible = !visible"
    >
      <EyeOff v-if="visible" :size="16" />
      <Eye v-else :size="16" />
    </button>
  </div>
</template>
