import {
  admitOllamaModel,
  admitOpenAILocalModel,
  type OllamaAdmission,
  type OpenAILocalAdmission,
  type Provider,
  planContextAdmitted,
} from "@local-agent/runtime";

/** What the check asks of the network (fakes in tests). */
export interface ModelCheckDeps {
  plan?: (
    provider: Provider,
    signal: AbortSignal,
  ) => Promise<{ provider: Provider; admission?: OllamaAdmission }>;
  admit?: (
    provider: Provider,
    signal: AbortSignal,
  ) => Promise<OllamaAdmission | OpenAILocalAdmission>;
}

/**
 * The provider a run starts on: a context planned for the model when the
 * user set none (planContext). With `admit`, as an interactive client needs,
 * the model must also exist at the endpoint, or this throws the line to
 * print; the plan's own admission, when it made one, already answers.
 * Headless runs plan only: the agent's own admission reports the rest.
 */
export async function checkModel(
  resolved: Provider,
  signal: AbortSignal,
  admit: boolean,
  deps: ModelCheckDeps = {},
): Promise<Provider> {
  const { provider, admission: planned } = await (
    deps.plan ?? planContextAdmitted
  )(resolved, signal);
  if (!admit) return provider;
  const admission =
    planned ??
    (await (
      deps.admit ??
      (provider.type === "openai_local"
        ? admitOpenAILocalModel
        : admitOllamaModel)
    )(provider, signal));
  if (admission.status === "rejected") throw new Error(admission.message);
  return provider;
}

/**
 * How `promise` stands right now: its value or its error, or undefined while
 * it is pending. Costs one turn of the event loop, never waits for the
 * promise.
 */
export async function settledNow<T>(
  promise: Promise<T>,
): Promise<{ value: T } | { error: unknown } | undefined> {
  const pending = Symbol("pending");
  const first = await Promise.race([
    promise.then(
      (value) => ({ value }),
      (error: unknown) => ({ error }),
    ),
    new Promise<typeof pending>((resolve) =>
      setImmediate(() => resolve(pending)),
    ),
  ]);
  return first === pending ? undefined : first;
}
