<script setup>
import { AlertTriangle, X } from 'lucide-vue-next'
import { confirmState, resolveConfirm } from '../stores/confirm'

function close(value) {
  resolveConfirm(value)
}
</script>

<template>
  <div v-if="confirmState.open" class="modal-backdrop confirm-backdrop" @click.self="close(false)">
    <div class="modal confirm-dialog" role="alertdialog" aria-modal="true">
      <div class="modal-head">
        <div class="confirm-dialog-title">
          <AlertTriangle v-if="confirmState.danger" :size="18" />
          <h2>{{ confirmState.title }}</h2>
        </div>
        <button class="icon-btn" type="button" aria-label="关闭" @click="close(false)">
          <X :size="17" />
        </button>
      </div>
      <div class="modal-body">
        <p>{{ confirmState.message }}</p>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="close(false)">{{ confirmState.cancelText }}</button>
        <button class="btn" :class="{ danger: confirmState.danger }" type="button" @click="close(true)">
          {{ confirmState.confirmText }}
        </button>
      </div>
    </div>
  </div>
</template>
