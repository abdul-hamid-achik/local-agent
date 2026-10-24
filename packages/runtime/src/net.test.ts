import { expect, test } from "vitest";
import { assertFetchTarget, fetchPolicy, isPublicAddress } from "#runtime/net";

test("isPublicAddress keeps public unicast and refuses every local range", () => {
  for (const ip of [
    "93.184.215.14",
    "8.8.8.8",
    "1.1.1.1",
    "172.32.0.1",
    "100.128.0.1",
    "2606:4700::1111",
    "2001:4860:4860::8888",
    "64:ff9b::808:808", // NAT64 of 8.8.8.8
    "2002:808:808::1", // 6to4 of 8.8.8.8
  ])
    expect(isPublicAddress(ip), ip).toBe(true);
  for (const ip of [
    "0.0.0.0",
    "10.0.0.1",
    "127.0.0.1",
    "127.255.255.254",
    "100.64.0.1",
    "169.254.169.254",
    "172.16.0.1",
    "172.31.255.255",
    "192.168.0.1",
    "192.0.0.8",
    "198.18.0.1",
    "224.0.0.1",
    "255.255.255.255",
    "::",
    "::1",
    "::ffff:127.0.0.1",
    "::ffff:7f00:1",
    "::ffff:169.254.169.254",
    "::127.0.0.1",
    "64:ff9b::7f00:1",
    "64:ff9b:1::1",
    "2002:7f00:1::1",
    "2001:0:4136:e378::1",
    "fc00::1",
    "fd12:3456::1",
    "fe80::1",
    "fe80::1%en0",
    "fec0::1",
    "ff02::1",
    "not-an-ip",
    "",
  ])
    expect(isPublicAddress(ip), ip).toBe(false);
});

const publicOnly = fetchPolicy({ resolve: async () => ["93.184.215.14"] }, {});

test("assertFetchTarget allows only http(s), whatever the address", async () => {
  for (const url of [
    "file:///etc/passwd",
    "data:text/plain,hi",
    "blob:https://a.example/x",
    "ftp://a.example/",
  ]) {
    await expect(assertFetchTarget(new URL(url), publicOnly)).rejects.toThrow(
      "unsupported protocol",
    );
    await expect(
      assertFetchTarget(new URL(url), { ...publicOnly, allowPrivate: true }),
    ).rejects.toThrow("unsupported protocol");
  }
  await assertFetchTarget(new URL("https://docs.example/"), publicOnly);
  await assertFetchTarget(new URL("http://93.184.215.14/"), publicOnly);
});

test("assertFetchTarget refuses a host when any resolved address is local", async () => {
  const policy = fetchPolicy(
    { resolve: async () => ["93.184.215.14", "::1"] },
    {},
  );
  await expect(
    assertFetchTarget(new URL("https://rebind.example/"), policy),
  ).rejects.toThrow("(::1)");
  await expect(
    assertFetchTarget(new URL("http://app.localhost/"), publicOnly),
  ).rejects.toThrow("private or loopback");
  await expect(
    assertFetchTarget(
      new URL("https://gone.example/"),
      fetchPolicy({ resolve: async () => [] }, {}),
    ),
  ).rejects.toThrow("cannot resolve gone.example");
  await expect(
    assertFetchTarget(
      new URL("https://down.example/"),
      fetchPolicy(
        {
          resolve: async () => {
            throw new Error("ENOTFOUND");
          },
        },
        {},
      ),
    ),
  ).rejects.toThrow("cannot resolve down.example (ENOTFOUND)");
});

test("LOCAL_AGENT_FETCH_PRIVATE=1 opts into loopback and private hosts", async () => {
  const optedIn = fetchPolicy({}, { LOCAL_AGENT_FETCH_PRIVATE: "1" });
  expect(optedIn.allowPrivate).toBe(true);
  await assertFetchTarget(new URL("http://127.0.0.1:3000/"), optedIn);
  expect(
    fetchPolicy({}, { LOCAL_AGENT_FETCH_PRIVATE: "true" }).allowPrivate,
  ).toBe(false);
  expect(
    fetchPolicy({ allowPrivate: false }, { LOCAL_AGENT_FETCH_PRIVATE: "1" })
      .allowPrivate,
  ).toBe(false);
});

test("special-purpose and translated ranges fail closed", () => {
  for (const ip of [
    "192.0.2.1",
    "198.51.100.7",
    "203.0.113.9",
    "192.88.99.1",
    "240.0.0.1",
    "::ffff:0:127.0.0.1", // SIIT translated loopback
    "::ffff:0:a9fe:a9fe", // SIIT translated metadata
    "100::1", // discard
    "2001:db8::1",
    "2001:10::1",
    "2001:20::1",
    "3fff::1",
    "5f00::1",
    "4000::1", // outside 2000::/3
    "::2",
  ])
    expect(isPublicAddress(ip), ip).toBe(false);
  expect(isPublicAddress("::ffff:0:808:808")).toBe(true);
  expect(isPublicAddress("2a00:1450::1")).toBe(true);
});

test("a trailing root dot changes neither the deny rule nor the localhost check", async () => {
  const seen: string[] = [];
  const policy = fetchPolicy(
    {
      resolve: async () => ["93.184.215.14"],
      deniedHost: async (host) => {
        seen.push(host);
        return host === "tracker.example"
          ? "domain:tracker.example"
          : undefined;
      },
    },
    {},
  );
  await expect(
    assertFetchTarget(new URL("https://tracker.example./x"), policy),
  ).rejects.toThrow("denied by rule");
  await expect(
    assertFetchTarget(new URL("http://localhost./"), policy),
  ).rejects.toThrow("private or loopback");
  await expect(
    assertFetchTarget(new URL("http://app.localhost../"), policy),
  ).rejects.toThrow("private or loopback");
  expect(seen).toEqual(["tracker.example", "localhost", "app.localhost"]);
});

test("an abort ends a stalled lookup at once", async () => {
  const controller = new AbortController();
  const policy = fetchPolicy(
    { resolve: () => new Promise<string[]>(() => {}) }, // never answers
    {},
  );
  const pending = assertFetchTarget(
    new URL("https://slow.example/"),
    policy,
    controller.signal,
  );
  controller.abort(new Error("stop"));
  await expect(pending).rejects.toThrow("stop");
  await expect(
    assertFetchTarget(
      new URL("https://slow.example/"),
      policy,
      controller.signal,
    ),
  ).rejects.toThrow("stop");
});
