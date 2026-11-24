<script setup>
defineProps({ title: { type: String, default: '' }, sub: { type: String, default: '' } });
</script>

<template>
  <div class="ph">
    <div>
      <h1 v-html="title" />
      <div class="sub" v-html="sub" />
    </div>
    <div class="spacer" />
    <slot />
  </div>
</template>
