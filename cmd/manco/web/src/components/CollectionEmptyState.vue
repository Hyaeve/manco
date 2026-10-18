<script setup>
import { BookMarked, BookOpen, Cat, FileArchive, FolderOpen, Rss } from 'lucide-vue-next'

defineProps({
  variant: { type: String, default: 'local' },
  windowTitle: { type: String, default: '' },
  heading: { type: String, default: '' },
  description: { type: String, default: '' },
})
</script>

<template>
  <section class="collection-empty" :class="`collection-empty--${variant}`">
    <header class="collection-empty-bar">
      <span class="collection-empty-dots" aria-hidden="true">
        <i />
        <i />
        <i />
      </span>
      <strong>{{ windowTitle }}</strong>
      <component :is="variant === 'local' ? FolderOpen : variant === 'tasks' ? FileArchive : Rss" :size="17" />
    </header>

    <div class="collection-empty-scene" aria-hidden="true">
      <template v-if="variant === 'local'">
        <div class="empty-cat empty-cat-orange">
          <img src="/logo-icon.png" alt="" />
        </div>
        <div class="empty-cat empty-cat-tabby">
          <Cat :size="42" :stroke-width="1.7" />
        </div>
        <div class="empty-comic-stack">
          <span class="comic-spine comic-spine-orange" />
          <span class="comic-spine comic-spine-blue" />
          <span class="comic-spine comic-spine-cream">
            <BookOpen :size="16" />
          </span>
        </div>
      </template>
      <template v-else-if="variant === 'tasks'">
        <div class="empty-task-cat">
          <Cat :size="50" :stroke-width="1.6" />
        </div>
        <span class="empty-grass left">
          <i />
          <i />
          <i />
        </span>
        <span class="empty-cobble-path">
          <i />
          <i />
          <i />
          <i />
          <i />
        </span>
        <span class="empty-grass right">
          <i />
          <i />
          <i />
        </span>
      </template>
      <template v-else>
        <div class="empty-cat empty-cat-mono">
          <Cat :size="44" :stroke-width="1.65" />
        </div>
        <div class="empty-cat empty-cat-tabby">
          <Cat :size="40" :stroke-width="1.7" />
        </div>
        <div class="empty-book-stack">
          <BookMarked :size="28" />
          <span class="book-spine book-spine-green" />
          <span class="book-spine book-spine-gold" />
        </div>
      </template>
      <span class="empty-shelf" />
    </div>

    <div class="collection-empty-copy">
      <h2>{{ heading }}</h2>
      <p>{{ description }}</p>
      <div v-if="$slots.actions" class="collection-empty-actions">
        <slot name="actions" />
      </div>
    </div>
  </section>
</template>

<style scoped>
.collection-empty {
  position: relative;
  width: min(100%, 760px);
  min-height: 340px;
  margin: 24px auto;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  box-shadow: var(--shadow-sm);
}

.collection-empty-bar {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  min-height: 43px;
  padding: 0 14px;
  border-bottom: 1px solid var(--border);
  background: color-mix(in srgb, var(--surface-muted) 82%, var(--surface));
  color: var(--text-muted);
}

.collection-empty-bar strong {
  overflow: hidden;
  color: var(--text);
  font-size: 13px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.collection-empty-dots {
  display: inline-flex;
  gap: 5px;
}

.collection-empty-dots i {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--primary) 58%, #d8d3cb);
}

.collection-empty-dots i:nth-child(2) {
  background: #a8c5ad;
}

.collection-empty-dots i:nth-child(3) {
  background: #a8b8d0;
}

.collection-empty-scene {
  position: relative;
  display: flex;
  min-height: 176px;
  align-items: flex-end;
  justify-content: center;
  gap: 14px;
  padding: 30px 20px 36px;
  background:
    radial-gradient(circle at 50% 30%, color-mix(in srgb, var(--primary-soft) 58%, transparent), transparent 62%),
    linear-gradient(180deg, transparent, color-mix(in srgb, var(--surface-muted) 38%, transparent));
}

.empty-cat {
  position: relative;
  z-index: 1;
  display: grid;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--border) 74%, transparent);
  box-shadow: 0 8px 18px rgba(58, 44, 29, 0.1);
}

.empty-cat-orange {
  width: 108px;
  height: 108px;
  border-radius: 28px;
  background: #fff1dc;
}

.empty-cat-orange img {
  width: 96px;
  height: 96px;
  object-fit: contain;
}

.empty-cat-tabby {
  width: 62px;
  height: 62px;
  border-radius: 20px;
  color: #4c3423;
  background:
    repeating-linear-gradient(90deg, #d2ae7b 0 6px, #b98750 6px 10px);
}

.empty-cat-mono {
  width: 66px;
  height: 66px;
  border-radius: 21px;
  color: #fff;
  background: linear-gradient(90deg, #343330 0 50%, #f5f1e8 50%);
  filter: drop-shadow(0 1px 0 rgba(0, 0, 0, 0.24));
}

.empty-comic-stack,
.empty-book-stack {
  position: relative;
  z-index: 1;
  display: flex;
  height: 74px;
  align-items: flex-end;
  gap: 4px;
  color: var(--text-muted);
}

.comic-spine,
.book-spine {
  display: grid;
  width: 22px;
  place-items: center;
  border-radius: 4px 4px 2px 2px;
  box-shadow: inset -2px 0 0 rgba(255, 255, 255, 0.28);
}

.comic-spine-orange {
  height: 64px;
  background: #e7a764;
}

.comic-spine-blue {
  height: 70px;
  background: #91adc3;
}

.comic-spine-cream {
  width: 28px;
  height: 58px;
  color: #6f5a3f;
  background: #f3e8ce;
}

.empty-book-stack {
  width: 82px;
  align-items: center;
  justify-content: center;
  color: #7d6845;
}

.empty-book-stack > svg {
  position: absolute;
  top: 0;
  left: 4px;
}

.book-spine {
  position: absolute;
  bottom: 0;
  width: 30px;
}

.book-spine-green {
  left: 12px;
  height: 54px;
  background: #93aa8d;
}

.book-spine-gold {
  right: 8px;
  height: 64px;
  background: #d8b96a;
}

.empty-shelf {
  position: absolute;
  right: 16%;
  bottom: 25px;
  left: 16%;
  height: 7px;
  border-radius: 999px;
  background: color-mix(in srgb, #9a744b 38%, var(--border));
  box-shadow: 0 9px 16px rgba(76, 57, 36, 0.12);
}

.empty-task-cat {
  position: relative;
  z-index: 2;
  display: grid;
  width: 102px;
  height: 102px;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--border) 68%, transparent);
  border-radius: 50% 50% 42% 42%;
  color: #f5f1e8;
  background: linear-gradient(90deg, #343330 0 50%, #e8e1d5 50%);
  box-shadow: 0 14px 25px rgba(56, 44, 30, 0.16);
}

.empty-task-cat::after {
  position: absolute;
  right: 13px;
  bottom: 12px;
  width: 22px;
  height: 10px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.32);
  content: '';
}

.empty-grass {
  position: absolute;
  bottom: 34px;
  display: flex;
  align-items: flex-end;
  gap: 5px;
}

.empty-grass.left {
  left: 12%;
}

.empty-grass.right {
  right: 12%;
}

.empty-grass i {
  display: block;
  width: 4px;
  height: 24px;
  border-radius: 999px 999px 0 0;
  background: color-mix(in srgb, #6e9b62 70%, var(--text-muted));
  transform: rotate(-18deg);
  transform-origin: bottom;
}

.empty-grass i:nth-child(2) {
  height: 34px;
  transform: rotate(5deg);
}

.empty-grass i:nth-child(3) {
  height: 20px;
  transform: rotate(20deg);
}

.empty-cobble-path {
  position: absolute;
  right: 16%;
  bottom: 27px;
  left: 16%;
  height: 16px;
  border-radius: 999px;
  background: color-mix(in srgb, #b8ad97 34%, var(--border));
}

.empty-cobble-path i {
  position: absolute;
  top: 2px;
  width: 9px;
  height: 6px;
  border-radius: 50%;
  background: color-mix(in srgb, #857b6d 48%, var(--surface));
}

.empty-cobble-path i:nth-child(1) { left: 7%; }
.empty-cobble-path i:nth-child(2) { left: 27%; top: 7px; }
.empty-cobble-path i:nth-child(3) { left: 49%; }
.empty-cobble-path i:nth-child(4) { left: 70%; top: 7px; }
.empty-cobble-path i:nth-child(5) { left: 88%; }

.collection-empty-copy {
  display: grid;
  justify-items: center;
  gap: 8px;
  padding: 0 24px 26px;
  text-align: center;
}

.collection-empty-copy h2 {
  margin: 0;
  color: var(--text);
  font-size: 18px;
  font-weight: 700;
}

.collection-empty-copy p {
  max-width: 520px;
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.65;
}

.collection-empty-actions {
  margin-top: 8px;
}

[data-theme='dark'] .collection-empty-bar {
  background: #302a22;
}

[data-theme='dark'] .empty-cat-orange {
  background: #4a3824;
}

[data-theme='dark'] .empty-cat-tabby {
  color: #f7e7ce;
  background: repeating-linear-gradient(90deg, #72583b 0 6px, #4f3b28 6px 10px);
}

[data-theme='dark'] .empty-task-cat {
  color: #29231e;
  background: linear-gradient(90deg, #d8d0c4 0 50%, #34302b 50%);
}

[data-theme='dark'] .empty-cobble-path {
  background: color-mix(in srgb, #6e6457 42%, var(--border));
}

[data-theme='dark'] .comic-spine-cream {
  color: #e9dcc2;
  background: #67553f;
}

@media (max-width: 640px) {
  .collection-empty-scene {
    gap: 9px;
    padding-inline: 12px;
  }

  .empty-cat-orange {
    width: 88px;
    height: 88px;
  }

  .empty-cat-orange img {
    width: 78px;
    height: 78px;
  }

  .empty-cat-tabby {
    width: 52px;
    height: 52px;
  }
}
</style>
