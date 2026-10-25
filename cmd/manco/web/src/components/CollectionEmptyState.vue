<script setup>
import { FileArchive, FolderOpen, Rss } from 'lucide-vue-next'

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
      <svg v-if="variant === 'subscriptions'" class="empty-illustration" viewBox="0 0 420 220" fill="none">
        <ellipse cx="210" cy="193" rx="150" ry="14" fill="rgba(77, 58, 37, 0.11)" />
        <path d="M42 188c45-24 80-22 116-7 28 12 55 13 92 1 54-19 90-15 130 7" stroke="rgba(113, 91, 62, 0.2)" stroke-width="4" stroke-linecap="round" />

        <path d="M317 72h-72a18 18 0 0 0-18 18v90h108V90a18 18 0 0 0-18-18Z" fill="#e1ab66" stroke="#493728" stroke-width="4" />
        <path d="M227 88h108v30H227z" fill="#f3ddbd" stroke="#493728" stroke-width="4" />
        <path d="M281 102h18v10h-18z" fill="#6f5038" />
        <path d="M326 82V57h16v25" stroke="#493728" stroke-width="4" stroke-linejoin="round" />
        <path d="M341 57h19v25h-19z" fill="#d56643" stroke="#493728" stroke-width="3" />
        <path d="M244 137h74v33h-74z" fill="#f8ead1" stroke="#493728" stroke-width="3" />
        <path d="M255 148h51M255 157h34" stroke="#a07b55" stroke-width="3" stroke-linecap="round" />

        <path d="M170 171c-28 1-41-14-34-27 7-12 24-8 31 4" stroke="#2e3031" stroke-width="13" stroke-linecap="round" />
        <ellipse cx="164" cy="135" rx="54" ry="58" fill="#303233" />
        <circle cx="164" cy="82" r="43" fill="#303233" />
        <path d="m132 56-5-31 30 19M190 46l28-20-3 33" fill="#303233" stroke="#303233" stroke-width="5" stroke-linejoin="round" />
        <path d="m136 41 11 12M204 43l-13 12" stroke="#d99a8e" stroke-width="5" stroke-linecap="round" />
        <ellipse cx="164" cy="91" rx="29" ry="24" fill="#3b3d3e" />
        <ellipse cx="149" cy="78" rx="8" ry="10" fill="#f5e78a" />
        <ellipse cx="179" cy="78" rx="8" ry="10" fill="#f5e78a" />
        <path d="M149 78h.1M179 78h.1" stroke="#252627" stroke-width="5" stroke-linecap="round" />
        <path d="m159 93 5-4 5 4-5 4Z" fill="#d58d83" />
        <path d="M164 97v6M164 103c-5 7-12 7-16 1M164 103c5 7 12 7 16 1M126 91l-20-4M126 99l-21 5M202 91l20-4M202 99l21 5" stroke="#e4d7c7" stroke-width="2.5" stroke-linecap="round" />
        <path d="M179 120c21-9 34-5 43 10l-12 22h-39Z" fill="#f7f1e6" stroke="#493728" stroke-width="3" />
        <path d="m181 123 22 17 21-17M203 140v16" stroke="#b28a61" stroke-width="2.5" />
        <ellipse cx="145" cy="157" rx="14" ry="8" fill="#3b3d3e" />
        <ellipse cx="185" cy="157" rx="14" ry="8" fill="#3b3d3e" />
      </svg>

      <svg v-else-if="variant === 'tasks'" class="empty-illustration" viewBox="0 0 420 220" fill="none">
        <ellipse cx="210" cy="191" rx="154" ry="15" fill="rgba(77, 58, 37, 0.1)" />
        <path d="M35 181c70-28 112-15 172 2 55 16 103 13 178-9" stroke="#b9ad99" stroke-width="14" stroke-linecap="round" />
        <path d="M54 177c58-21 103-13 150 1 48 14 92 12 160-5" stroke="#d6cbb8" stroke-width="5" stroke-linecap="round" stroke-dasharray="17 13" />
        <path d="M61 154c6-24 13-37 23-50M73 156c2-29 8-44 17-56M87 158c7-23 13-34 24-44M333 155c-8-24-16-35-29-45M348 158c-10-25-21-38-34-47" stroke="#74a267" stroke-width="4" stroke-linecap="round" />

        <path d="M280 103h70a12 12 0 0 1 12 12v53h-94v-53a12 12 0 0 1 12-12Z" fill="#d9ad6d" stroke="#4b3b2d" stroke-width="4" />
        <path d="M268 126h94" stroke="#4b3b2d" stroke-width="4" />
        <circle cx="315" cy="117" r="14" fill="#73a36d" stroke="#4b3b2d" stroke-width="3" />
        <path d="m308 117 5 5 9-10" stroke="#fffaf0" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" />

        <path d="M186 171c-26 8-52 2-64-13-10-13-3-27 11-31" stroke="#fbf8f0" stroke-width="16" stroke-linecap="round" />
        <ellipse cx="178" cy="139" rx="55" ry="40" fill="#fbf8f0" />
        <circle cx="213" cy="107" r="42" fill="#fbf8f0" stroke="#5b5148" stroke-width="3" />
        <path d="m181 84-5-29 28 19M237 76l25-19-2 31" fill="#fbf8f0" stroke="#5b5148" stroke-width="4" stroke-linejoin="round" />
        <path d="m185 67 10 12M249 67l-13 12" stroke="#e9af9b" stroke-width="4" stroke-linecap="round" />
        <path d="M187 91c8-7 14-7 21 0M222 91c8-7 14-7 21 0" stroke="#4d4742" stroke-width="3.5" stroke-linecap="round" />
        <path d="m211 102 5-4 5 4-5 4Z" fill="#d9948b" />
        <path d="M211 106v4M211 110c-5 5-11 5-15 0M211 110c5 5 11 5 15 0" stroke="#6e6257" stroke-width="2" stroke-linecap="round" />
        <path d="M170 94c12-8 23-8 34 0" stroke="#e4aa63" stroke-width="12" stroke-linecap="round" />
        <ellipse cx="198" cy="157" rx="14" ry="7" fill="#f0ece3" />
        <ellipse cx="152" cy="157" rx="14" ry="7" fill="#f0ece3" />
        <path d="M230 137c22 4 34 16 31 30" stroke="#fbf8f0" stroke-width="13" stroke-linecap="round" />
      </svg>

      <svg v-else class="empty-illustration" viewBox="0 0 420 220" fill="none">
        <ellipse cx="210" cy="193" rx="156" ry="16" fill="rgba(77, 58, 37, 0.11)" />
        <path d="M65 183h290" stroke="#946b43" stroke-width="10" stroke-linecap="round" />
        <path d="M78 180V86h46v94M296 180V70h48v110" fill="#d9b57b" stroke="#5d4632" stroke-width="4" />
        <path d="M76 86h50M294 70h52" stroke="#5d4632" stroke-width="5" stroke-linecap="round" />
        <path d="M86 180v-36h24v36M304 180v-45h26v45" stroke="#9b7046" stroke-width="4" />
        <path d="M88 101h26M88 114h26M306 84h26M306 98h26" stroke="#806044" stroke-width="3" stroke-linecap="round" />

        <path d="M128 179v-48h18v48M147 179V86h22v93M306 179v-60h17v60M324 179V94h20v85" fill="#7fa08b" stroke="#4c594a" stroke-width="2.5" />
        <path d="M153 95h10M153 104h10M311 101h8M329 109h9" stroke="#e5dcc5" stroke-width="2" stroke-linecap="round" />

        <ellipse cx="222" cy="168" rx="53" ry="25" fill="#e49a49" />
        <circle cx="222" cy="112" r="48" fill="#e49a49" />
        <path d="m187 82-7-38 38 24M252 72l37-25-4 42" fill="#e49a49" stroke="#5a4030" stroke-width="4" stroke-linejoin="round" />
        <path d="m196 62 12 15M271 60l-14 17" stroke="#c97842" stroke-width="4" stroke-linecap="round" />
        <path d="M193 99c8-7 16-7 24 0M228 99c8-7 16-7 24 0" stroke="#4e392a" stroke-width="3.5" stroke-linecap="round" />
        <path d="m217 112 6-4 6 4-6 5Z" fill="#cf7b78" />
        <path d="M223 117v5M223 122c-5 5-12 5-16 0M223 122c5 5 12 5 16 0" stroke="#714a35" stroke-width="2" stroke-linecap="round" />
        <path d="M187 108h-23M188 117l-24 6M258 108h23M257 117l24 6" stroke="#fff3d5" stroke-width="2.4" stroke-linecap="round" />
        <path d="M191 85c10-7 18-7 27 0M227 88c9-7 18-7 27 0M193 128c18 8 39 8 57 0" stroke="#b76935" stroke-width="7" stroke-linecap="round" />
        <ellipse cx="198" cy="170" rx="17" ry="9" fill="#e49a49" />
        <ellipse cx="245" cy="170" rx="17" ry="9" fill="#e49a49" />
        <path d="M257 166c29 2 39 16 29 31" stroke="#e49a49" stroke-width="14" stroke-linecap="round" />
        <path d="M194 140c7 15 7 26 2 37M222 142c5 14 4 25 0 36M249 140c-5 15-4 26 1 37" stroke="#d67f3f" stroke-width="5" stroke-linecap="round" />
      </svg>
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
  display: grid;
  min-height: 210px;
  place-items: center;
  padding: 14px 20px 4px;
  background:
    radial-gradient(circle at 50% 32%, color-mix(in srgb, var(--primary-soft) 58%, transparent), transparent 62%),
    linear-gradient(180deg, transparent, color-mix(in srgb, var(--surface-muted) 38%, transparent));
}

.empty-illustration {
  display: block;
  width: min(520px, 94%);
  height: auto;
  max-height: 205px;
}

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

[data-theme='dark'] .collection-empty-scene {
  background:
    radial-gradient(circle at 50% 32%, rgba(194, 132, 63, 0.13), transparent 62%),
    linear-gradient(180deg, transparent, rgba(255, 255, 255, 0.025));
}

@media (max-width: 640px) {
  .collection-empty-scene {
    min-height: 174px;
    padding: 10px 8px 0;
  }

  .empty-illustration {
    width: 100%;
    max-height: 170px;
  }

  .collection-empty-copy {
    padding-inline: 18px;
  }
}
</style>
