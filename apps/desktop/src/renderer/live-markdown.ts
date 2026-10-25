import { type Ref, ref, watch } from "vue";
import { renderMarkdown } from "#desktop/renderer/markdown";

/** How the live reply waits for the next frame (requestAnimationFrame). */
export interface Frames {
  request(callback: () => void): number;
  cancel(id: number): void;
}

/**
 * The live reply's HTML. Deltas can come faster than the screen redraws, and
 * each render covers the whole reply so far: it renders at most once a
 * frame, with the text as it is then. An emptied reply clears at once, so a
 * new one never opens on the last one's text.
 */
export function liveMarkdown(
  text: () => string,
  frames: Frames,
): { html: Ref<string>; stop(): void } {
  const html = ref("");
  let pending: number | undefined;
  const cancel = () => {
    if (pending !== undefined) frames.cancel(pending);
    pending = undefined;
  };
  const stopWatching = watch(
    text,
    (value) => {
      if (!value) {
        cancel();
        html.value = "";
        return;
      }
      pending ??= frames.request(() => {
        pending = undefined;
        html.value = renderMarkdown(text());
      });
    },
    { immediate: true, flush: "sync" },
  );
  return {
    html,
    stop() {
      stopWatching();
      cancel();
    },
  };
}
