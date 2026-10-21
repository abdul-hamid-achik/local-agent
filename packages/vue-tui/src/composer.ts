import {
  defineComponent,
  h,
  nextTick,
  onMounted,
  shallowRef,
  watch,
} from "vue";
import {
  Box,
  Textarea,
  type TextareaElement,
  type TextareaProps,
} from "vue-termui";

const composerKeyBindings = [
  { name: "return", action: "submit" },
  { name: "kpenter", action: "submit" },
  { name: "linefeed", action: "submit" },
  { name: "return", shift: true, action: "newline" },
  { name: "kpenter", shift: true, action: "newline" },
  { name: "linefeed", shift: true, action: "newline" },
] satisfies NonNullable<TextareaProps["keyBindings"]>;

export const Composer = defineComponent({
  name: "ComposerInput",
  props: {
    modelValue: { type: String, required: true },
    busy: { type: Boolean, default: false },
    height: { type: Number, default: 3 },
  },
  emits: {
    "update:modelValue": (value: string) => typeof value === "string",
    submit: (value: string) => typeof value === "string",
    cancel: () => true,
  },
  setup(props, { emit, expose }) {
    const editor = shallowRef<{ $el?: TextareaElement }>();
    expose({ focus: () => editor.value?.$el?.focus() });
    onMounted(() => {
      if (!props.busy) void nextTick(() => editor.value?.$el?.focus());
    });
    watch(editor, (value) => {
      if (!props.busy) value?.$el?.focus();
    });
    watch(
      () => props.busy,
      (busy) => {
        if (!busy) void nextTick(() => editor.value?.$el?.focus());
      },
      { flush: "post" },
    );
    watch(
      () => props.modelValue,
      (value) => {
        const native = editor.value?.$el;
        if (native && native.plainText !== value) native.setText(value);
      },
    );
    const submit = (value: string) => {
      if (props.busy) return;
      emit("submit", value);
      // Paste and submit can share a Vue tick; reconcile even if the prop
      // returned to its previous value and its watcher did not fire.
      void nextTick(() => {
        const native = editor.value?.$el;
        if (native && native.plainText !== props.modelValue)
          native.setText(props.modelValue);
      });
    };
    const keyDown = (key: { name: string; preventDefault(): void }) => {
      if (key.name === "escape") {
        key.preventDefault();
        emit("cancel");
      }
    };

    return () =>
      h(
        Box,
        {
          border: true,
          flexDirection: "column",
          flexShrink: 0,
          height: Math.max(3, props.height),
        },
        () =>
          h(Textarea, {
            modelValue: props.modelValue,
            ref: editor,
            "onUpdate:modelValue": (value: string) =>
              emit("update:modelValue", value),
            onSubmit: submit,
            onKeyDown: keyDown,
            keyBindings: composerKeyBindings,
            focusable: !props.busy,
            autofocus: !props.busy,
            width: "100%",
            height: "100%",
            placeholder: props.busy ? "Generating…" : "Message the model",
          }),
      );
  },
});

export type ComposerProps = InstanceType<typeof Composer>["$props"];
