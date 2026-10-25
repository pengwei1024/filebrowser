<template>
  <div class="office-viewer">
    <div v-if="mode === 'loading'" class="office-center">
      <div class="spinner">
        <div class="bounce1"></div>
        <div class="bounce2"></div>
        <div class="bounce3"></div>
      </div>
      <p>{{ t("files.loading") }}</p>
    </div>

    <object
      v-else-if="mode === 'ready'"
      class="office-pdf"
      :data="pdfSrc"
      type="application/pdf"
    ></object>

    <div v-else class="office-center office-error">
      <i class="material-icons">feedback</i>
      <p>{{ errorMsg }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { files as api } from "@/api";
import { onBeforeUnmount, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";

const props = defineProps<{ path: string }>();
const { t } = useI18n();

type Mode = "loading" | "ready" | "error";
const mode = ref<Mode>("loading");
const errorMsg = ref<string>("");
const pdfSrc = ref<string>("");

onMounted(async () => {
  try {
    // The backend converts the document to PDF (cached) and returns it; we load
    // the compact PDF as a blob so a large source file is never shipped to the
    // browser, keeping preview fast over remote links.
    pdfSrc.value = await api.getOfficePreviewURL(props.path);
    mode.value = "ready";
  } catch (e: any) {
    mode.value = "error";
    errorMsg.value =
      e?.status === 501
        ? "Office preview is not available on the server."
        : "Failed to generate the document preview.";
  }
});

onBeforeUnmount(() => {
  if (pdfSrc.value) {
    URL.revokeObjectURL(pdfSrc.value);
  }
});
</script>

<style scoped>
.office-viewer,
.office-pdf {
  width: 100%;
  height: 100%;
}

.office-viewer {
  position: absolute;
  inset: 0;
}

.office-pdf {
  border: none;
}

.office-center {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 0.5rem;
  color: var(--fg);
}
</style>
