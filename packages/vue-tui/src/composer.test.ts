import { createTestRenderer } from "@opentui/core/testing";
import { afterEach, describe, expect, test } from "vitest";
import { createRenderer, defineComponent, h, nextTick, ref } from "vue";
import { createNodeOps, rendererInjectionKey } from "vue-termui";
import { Approval } from "./approval";
import { Composer } from "./composer";

const mounted: Array<{ unmount: () => void; destroy: () => void }> = [];

afterEach(() => {
  for (const app of mounted.splice(0)) {
    app.unmount();
    app.destroy();
  }
});

async function mount(component: ReturnType<typeof defineComponent>) {
  const terminal = await createTestRenderer({
    width: 80,
    height: 12,
    kittyKeyboard: true,
  });
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp(
    component,
  );
  app.provide(rendererInjectionKey, terminal.renderer);
  app.mount(terminal.renderer.root);
  mounted.push({
    unmount: () => app.unmount(),
    destroy: () => terminal.renderer.destroy(),
  });
  await nextTick();
  await terminal.flush();
  return terminal;
}

describe("Composer", () => {
  test("submits on Enter and keeps multiline paste in the draft", async () => {
    const draft = ref("");
    const submitted = ref<string[]>([]);
    const Root = defineComponent({
      setup: () => () =>
        h(Composer, {
          modelValue: draft.value,
          "onUpdate:modelValue": (value: string) => (draft.value = value),
          onSubmit: (value: string) => submitted.value.push(value),
        }),
    });
    const terminal = await mount(Root);

    await terminal.mockInput.typeText("hello");
    terminal.mockInput.pressEnter();
    await nextTick();
    expect(submitted.value).toEqual(["hello"]);

    await terminal.mockInput.pasteBracketedText("one\ntwo");
    await nextTick();
    expect(draft.value).toContain("one\ntwo");
  });

  test("Shift+Enter inserts a newline and Escape cancels", async () => {
    const draft = ref("");
    const cancelled = ref(0);
    const Root = defineComponent({
      setup: () => () =>
        h(Composer, {
          modelValue: draft.value,
          "onUpdate:modelValue": (value: string) => (draft.value = value),
          onCancel: () => cancelled.value++,
        }),
    });
    const terminal = await mount(Root);

    await terminal.mockInput.typeText("a");
    terminal.mockInput.pressEnter({ shift: true });
    await terminal.mockInput.typeText("b");
    terminal.mockInput.pressEscape();
    await nextTick();
    expect(draft.value).toBe("a\nb");
    expect(cancelled.value).toBe(1);
  });
});

test("Composer does not submit while busy", async () => {
  const submitted = ref(0);
  const Root = defineComponent({
    setup: () => () =>
      h(Composer, {
        modelValue: "draft",
        busy: true,
        onSubmit: () => submitted.value++,
      }),
  });
  const terminal = await mount(Root);
  terminal.mockInput.pressEnter();
  await nextTick();
  expect(submitted.value).toBe(0);
});

test("programmatic model changes replace the native draft", async () => {
  const draft = ref("before");
  const Root = defineComponent({
    setup: () => () =>
      h(Composer, {
        modelValue: draft.value,
        "onUpdate:modelValue": (value: string) => (draft.value = value),
      }),
  });
  const terminal = await mount(Root);

  draft.value = Array.from(
    { length: 100 },
    (_, index) => `line ${index + 1}`,
  ).join("\n");
  await nextTick();
  await terminal.flush();
  terminal.resize(40, 6);
  await terminal.flush();
  for (let index = 0; index < 100; index++)
    terminal.mockInput.pressArrow("down");
  await terminal.flush();
  expect(terminal.captureCharFrame()).toContain("line 100");

  draft.value = "";
  await nextTick();
  await terminal.flush();
  expect(terminal.captureCharFrame()).not.toContain("line 100");
});

test("approval swap removes the composer key listener and preserves the draft", async () => {
  const draft = ref("");
  const view = ref<"composer" | "approval">("composer");
  const submitted = ref(0);
  const decisions: boolean[] = [];
  const Root = defineComponent({
    setup: () => () =>
      view.value === "composer"
        ? h(Composer, {
            modelValue: draft.value,
            "onUpdate:modelValue": (value: string) => (draft.value = value),
            onSubmit: () => {
              submitted.value++;
              view.value = "approval";
            },
          })
        : h(Approval, {
            preview: draft.value,
            onDecision: (value: boolean) => {
              decisions.push(value);
              view.value = "composer";
            },
          }),
  });
  const terminal = await mount(Root);

  await terminal.mockInput.typeText("keep me");
  terminal.mockInput.pressEnter();
  await nextTick();
  terminal.mockInput.pressEnter();
  await nextTick();
  expect(submitted.value).toBe(1);
  expect(decisions).toEqual([false]);
  expect(draft.value).toBe("keep me");
});

test("a local command clears the native editor when paste and submit share a tick", async () => {
  const draft = ref("");
  const submitted: string[] = [];
  const terminal = await mount(
    defineComponent({
      setup: () => () =>
        h(Composer, {
          modelValue: draft.value,
          "onUpdate:modelValue": (value: string) => {
            draft.value = value;
          },
          onSubmit: (value: string) => {
            submitted.push(value);
            draft.value = "";
          },
        }),
    }),
  );
  await terminal.mockInput.pasteBracketedText("/mode plan");
  terminal.mockInput.pressEnter();
  await nextTick();
  await terminal.flush();
  expect(terminal.captureCharFrame()).not.toContain("/mode plan");
  await terminal.mockInput.pasteBracketedText("next");
  terminal.mockInput.pressEnter();
  await nextTick();
  expect(submitted).toEqual(["/mode plan", "next"]);
});
