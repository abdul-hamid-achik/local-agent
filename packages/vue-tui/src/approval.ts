import { defineComponent, h, ref, watch } from "vue";
import { Box, ScrollBox, Text } from "vue-termui";

export const Approval = defineComponent({
  name: "ApprovalDialog",
  props: {
    title: { type: String, default: "Approve change?" },
    preview: { type: String, default: "" },
    maxLines: { type: Number, default: 12 },
  },
  emits: { decision: (approved: boolean) => typeof approved === "boolean" },
  setup(props, { emit }) {
    // One decision per prompt: a repeated key must not decide twice, and a
    // new prompt on the same instance must be decidable again.
    const decided = ref(false);
    watch(
      () => [props.title, props.preview],
      () => {
        decided.value = false;
      },
    );
    const decide = (approved: boolean) => {
      if (decided.value) return;
      decided.value = true;
      emit("decision", approved);
    };
    const keyDown = (key: { name: string; preventDefault(): void }) => {
      if (key.name === "y") {
        key.preventDefault();
        decide(true);
      } else if (
        key.name === "n" ||
        key.name === "return" ||
        key.name === "escape"
      ) {
        key.preventDefault();
        decide(false);
      }
    };

    return () => {
      const lines = props.preview.split("\n");
      return h(
        Box,
        {
          border: true,
          flexDirection: "column",
        },
        () => [
          h(Text, { bold: true }, () => props.title),
          h(
            ScrollBox,
            {
              height: Math.max(1, props.maxLines),
              flexGrow: 0,
              minHeight: 1,
              focusable: true,
              autofocus: true,
              onKeyDown: keyDown,
            },
            () => lines.map((line) => h(Text, {}, () => line)),
          ),
          h(Text, {}, () => "Enter deny · y approve · n deny · Esc cancel"),
        ],
      );
    };
  },
});

export type ApprovalProps = InstanceType<typeof Approval>["$props"];
