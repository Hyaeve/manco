<script setup>
// 两段式排序选择器：上面选排序字段、下面选升/降序，样式沿用本地仓库详情页那套
// .chapter-sort-* 类，三处（详情页、订阅清单、本地仓库）外观保持一致。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ArrowDown, ArrowUp, Check } from 'lucide-vue-next'

const props = defineProps({
  // [{ value: 'title', label: '标题' }, ...]
  options: { type: Array, required: true },
  mode: { type: String, required: true },
  desc: { type: Boolean, default: true },
})
const emit = defineEmits(['change'])

const root = ref(null)
const open = ref(false)
const upward = ref(false)

const currentLabel = computed(() => props.options.find((item) => item.value === props.mode)?.label || props.options[0]?.label || '排序')

function toggle() {
  if (!open.value && root.value) {
    // 菜单大约 220px 高：下方放不下就向上弹。
    upward.value = root.value.getBoundingClientRect().bottom + 220 > window.innerHeight
  }
  open.value = !open.value
}

function chooseMode(mode) {
  open.value = false
  emit('change', { mode, desc: props.desc })
}

function chooseDirection(desc) {
  open.value = false
  emit('change', { mode: props.mode, desc })
}

function onOutside(event) {
  if (!root.value?.contains(event.target)) open.value = false
}

onMounted(() => window.addEventListener('pointerdown', onOutside))
onUnmounted(() => window.removeEventListener('pointerdown', onOutside))
</script>

<template>
  <div ref="root" class="chapter-sort-wrap" aria-label="排序">
    <button
      class="chapter-sort-trigger"
      type="button"
      aria-haspopup="menu"
      :aria-expanded="open"
      @click="toggle"
    >
      <span>{{ currentLabel }}</span>
      <component :is="desc ? ArrowDown : ArrowUp" :size="14" :stroke-width="1.6" />
    </button>
    <Transition name="select-popup">
      <div v-if="open" class="chapter-sort-menu" :class="upward ? 'above' : 'below'" role="menu">
        <div class="chapter-sort-group">
          <button
            v-for="option in options"
            :key="option.value"
            type="button"
            role="menuitemradio"
            :aria-checked="mode === option.value"
            :class="{ active: mode === option.value }"
            @click="chooseMode(option.value)"
          >
            <span>{{ option.label }}</span>
            <Check v-if="mode === option.value" class="chapter-sort-check" :size="15" />
          </button>
        </div>
        <div class="chapter-sort-group">
          <button type="button" role="menuitemradio" :aria-checked="!desc" :class="{ active: !desc }" @click="chooseDirection(false)">
            <ArrowUp :size="15" :stroke-width="1.6" />
            <span>升序</span>
            <Check v-if="!desc" class="chapter-sort-check" :size="15" />
          </button>
          <button type="button" role="menuitemradio" :aria-checked="desc" :class="{ active: desc }" @click="chooseDirection(true)">
            <ArrowDown :size="15" :stroke-width="1.6" />
            <span>降序</span>
            <Check v-if="desc" class="chapter-sort-check" :size="15" />
          </button>
        </div>
      </div>
    </Transition>
  </div>
</template>
