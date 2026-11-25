import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";

describe("api() client", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sends X-Requested-With and dispatches sweeparr:unauthorized on 401", async () => {
    const onUnauth = vi.fn();
    window.addEventListener("sweeparr:unauthorized", onUnauth);
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ detail: "Unauthorized" }), { status: 401 })
    );

    await expect(api("/api/servers")).rejects.toThrow("Unauthorized");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect((init.headers as Record<string, string>)["X-Requested-With"]).toBe("sweeparr");
    expect(onUnauth).toHaveBeenCalledTimes(1);
    window.removeEventListener("sweeparr:unauthorized", onUnauth);
  });

  it("does NOT dispatch sweeparr:unauthorized on /api/auth/login 401", async () => {
    const onUnauth = vi.fn();
    window.addEventListener("sweeparr:unauthorized", onUnauth);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ detail: "Invalid credentials" }), { status: 401 })
    );

    await expect(api("/api/auth/login", "POST", { username: "a", password: "b" })).rejects.toThrow("Invalid credentials");
    expect(onUnauth).not.toHaveBeenCalled();
    window.removeEventListener("sweeparr:unauthorized", onUnauth);
  });

  it("retries GET on 503 but never retries POST", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response("{}", { status: 503 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }));

    const res = await api<{ ok: boolean }>("/api/servers");
    expect(res.ok).toBe(true);
    expect(fetchSpy).toHaveBeenCalledTimes(2);

    fetchSpy.mockClear();
    fetchSpy.mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Unavailable" }), { status: 503 }));
    await expect(api("/api/servers", "POST", {})).rejects.toThrow("Unavailable");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });
});
