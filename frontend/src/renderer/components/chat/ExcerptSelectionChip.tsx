import { MessageSquareQuote } from "lucide-react";

/** The expansion deliberately reveals the selection, not the entire turn. */
export function ExcerptSelectionChip({ selection, role }: { selection: string; role?: string }) {
	const preview = selection.length > 80 ? `${selection.slice(0, 80)}…` : selection;
	return (
		<details className="min-w-0 max-w-full rounded border border-logo-accent/30 bg-logo-accent/8 px-2 py-1 text-[11px] text-muted-foreground">
			<summary className="flex cursor-pointer items-center gap-1.5" title={selection} aria-label={`Referenced selection: ${selection}`}>
				<MessageSquareQuote aria-hidden="true" className="size-3.5 shrink-0 text-logo-accent" />
				<span className="max-w-[280px] truncate">{role === "assistant" ? "Agent" : "You"}: “{preview}”</span>
			</summary>
			<p className="max-w-[min(70vw,36rem)] whitespace-pre-wrap break-words pt-2 text-foreground">{selection}</p>
		</details>
	);
}
