import { useEffect, useRef } from "react";
import { aoBridge } from "./bridge";

/**
 * Escape for a full-screen artifact viewer or an artifact preview, also from
 * inside its framed page. Keys pressed in a framed page never reach this
 * document, so its own Escape listeners cannot see them; while such a surface
 * is open, main reports Escape pressed anywhere in the shell instead.
 */

// Open surfaces, so one closing does not stop main reporting for another.
let armed = 0;

function arm() {
	armed += 1;
	if (armed === 1) aoBridge.artifactEscape.setArmed(true);
}

function disarm() {
	armed = Math.max(0, armed - 1);
	if (armed === 0) aoBridge.artifactEscape.setArmed(false);
}

/** The framed page inside `container` that holds focus, whose keys this document never sees. */
function focusedFramedPage(container: Element): Element | null {
	const active = document.activeElement;
	return (active instanceof HTMLIFrameElement || active?.tagName === "WEBVIEW") && container.contains(active) ? active : null;
}

/**
 * While `active`, handles Escape pressed with focus in a framed page inside
 * `container` as if it had been pressed on the frame itself: it is dispatched
 * there, so the topmost layer over the page takes it first. A menu opened over
 * the page while focus stayed in it closes, and the surface stays; with
 * nothing over the page, the surface's own Escape handling runs, and
 * `onEscape` is called only if none took it. Escape with focus anywhere else
 * already reaches this document, so it is left to its own listeners.
 */
export function useArtifactFrameEscape(active: boolean, container: () => Element | null, onEscape: () => void): void {
	const containerRef = useRef(container);
	containerRef.current = container;
	const onEscapeRef = useRef(onEscape);
	onEscapeRef.current = onEscape;
	useEffect(() => {
		if (!active) return;
		arm();
		const stop = aoBridge.artifactEscape.onEscape(() => {
			const element = containerRef.current();
			const page = element ? focusedFramedPage(element) : null;
			if (!page) return;
			const escape = new KeyboardEvent("keydown", { key: "Escape", code: "Escape", bubbles: true, cancelable: true, composed: true });
			if (page.dispatchEvent(escape)) onEscapeRef.current();
		});
		return () => {
			stop();
			disarm();
		};
	}, [active]);
}
