import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useIndependentSideChats } from "./IndependentSideChats";

const { get, post, put, remove } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), remove: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: { GET: get, POST: post, PUT: put, DELETE: remove },
	apiErrorMessage: () => "Send failed", getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {} }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { sideChats: { capture: async () => {} } } }));

describe("side delivery draft ownership", () => {
	let accept: (value: { error?: object }) => void;
	let reject: (error: Error) => void;
	beforeEach(() => {
		vi.clearAllMocks();
		get.mockImplementation(async (path: string) => {
			if (path.endsWith("/side-chats")) return { data: { sides: [{ id: "side", sessionId: "session", state: "ready" }] } };
			if (path.endsWith("/draft")) return { data: { contentJson: "" } };
			return { data: { snapshot: { side: { id: "side", state: "ready" }, messages: [], turns: [], activities: [] } } };
		});
		post.mockImplementation(() => new Promise((resolve, fail) => { accept = resolve; reject = fail; }));
		put.mockResolvedValue({});
		remove.mockResolvedValue({});
	});

	async function beginSend(text = "first") {
		const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
		await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
		act(() => hook.result.current.setQuestionDraft("side", text));
		let sending!: Promise<void>;
		act(() => { sending = hook.result.current.send("side", text); });
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		return { ...hook, sending };
	}

	it("preserves a reference added while acceptance is pending", async () => {
		const hook = await beginSend();
		act(() => hook.result.current.addReference("side", { id: "chip", conversationId: "main", messageId: "message", revision: 1, text: "sun", role: "assistant" }));
		await act(async () => { accept({}); await hook.sending; });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => {
			const draft = JSON.parse(options.body.contentJson);
			return draft.text === "first" && draft.references[0]?.text === "sun";
		})).toBe(true));
	});

	it("clears only an unchanged submitted draft", async () => {
		const hook = await beginSend();
		await act(async () => { accept({}); await hook.sending; });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => JSON.parse(options.body.contentJson).text === "")).toBe(true));
	});

	it("keeps the draft on failure", async () => {
		const hook = await beginSend();
		await act(async () => { accept({ error: {} }); await hook.sending; });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => JSON.parse(options.body.contentJson).text === "first")).toBe(true));
	});

	it("clears a submitted /btw question without sending the command", async () => {
		const hook = await beginSend("/btw first");
		expect(post.mock.calls[0][1].body.text).toBe("first");
		await act(async () => { accept({}); await hook.sending; });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => JSON.parse(options.body.contentJson).text === "")).toBe(true));
	});
	it("reuses an uncertain receipt and preserves a newer draft", async () => {
		const hook = await beginSend("original");
		const original = post.mock.calls[0][1].body;
		await act(async () => { reject(new Error("response lost")); await hook.sending; });
		act(() => hook.result.current.setQuestionDraft("side", "next question"));
		let retry!: Promise<void>;
		act(() => { retry = hook.result.current.send("side", ""); });
		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[1][1].body).toEqual(original);
		await act(async () => { accept({}); await retry; });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => {
			const draft = JSON.parse(options.body.contentJson);
			return draft.text === "next question" && !draft.pendingDelivery;
		})).toBe(true));
	});

	it("saves the exact receipt before dispatch and restores it on remount", async () => {
		const hook = await beginSend("original");
		const frozen = post.mock.calls[0][1].body;
		const saved = put.mock.calls.find(([, options]) => JSON.parse(options.body.contentJson).pendingDelivery)?.[1].body.contentJson;
		expect(saved).toBeDefined();
		expect(put.mock.invocationCallOrder[0]).toBeLessThan(post.mock.invocationCallOrder[0]);
		await act(async () => { reject(new Error("lost")); await hook.sending; });
		hook.unmount();
		const oldGet = get.getMockImplementation()!;
		get.mockImplementation(async (path: string) => path.endsWith("/draft") ? { data: { contentJson: saved } } : oldGet(path));
		const recovered = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
		await waitFor(() => expect(recovered.result.current.activeId).toBe("side"));
		await waitFor(() => expect(get.mock.calls.filter(([path]) => path.endsWith("/draft")).length).toBeGreaterThan(1));
		let retry!: Promise<void>;
		act(() => { retry = recovered.result.current.send("side", ""); });
		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[1][1].body).toEqual(frozen);
		await act(async () => { accept({}); await retry; });
	});

	it("retries image delivery without restaging its attachment", async () => {
		const stage = vi.fn(async () => ["/scratch/image.png"]);
		const hook = renderHook(() => useIndependentSideChats("session", [], [], stage, true));
		await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
		act(() => hook.result.current.setQuestionDraft("side", "image question"));
		let sending!: Promise<void>;
		act(() => { sending = hook.result.current.send("side", "image question", [{ mimeType: "image/png", data: "cGl4ZWxz" }]); });
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		const original = post.mock.calls[0][1].body;
		expect(original.attachments).toEqual([{ mimeType: "image/png", data: "cGl4ZWxz" }]);
		await act(async () => { reject(new Error("response lost")); await sending; });
		let retry!: Promise<void>;
		act(() => { retry = hook.result.current.send("side", ""); });
		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[1][1].body).toEqual(original);
		expect(stage).toHaveBeenCalledOnce();
		await act(async () => { accept({}); await retry; });
	});

	it("does not recreate a closed draft after late acceptance", async () => {
		const hook = await beginSend("original");
		await act(async () => { await hook.result.current.close("side"); });
		const savedCount = put.mock.calls.length;
		await act(async () => { accept({}); await hook.sending; });
		expect(hook.result.current.sides).toEqual([]);
		expect(put.mock.calls.length).toBe(savedCount);
	});

});
