// The two small helpers of the conversation, as plain code with no DOM (node runs it in test/wait-bar.test.ts): when the
// arrow to the last message is shown, and where the card that waits for the user is (index.ts draws the bar and the arrow).

export const JUMP_AT = 80; // px above the bottom after which the arrow to the last message is shown

// The conversation is drawn newest at the bottom with `column-reverse`: scrollTop is 0 at the bottom and goes negative
// (Chrome, Firefox, Safari) or positive (older engines) as the user scrolls up, so only its size counts.
export function jumpVisible(scrollTop: number, scrollable: boolean): boolean {
  return scrollable && Math.abs(scrollTop) > JUMP_AT;
}

export interface Span {
  top: number;
  bottom: number;
}

// Whether the card that waits is in view, and if not, on which side: "in" when most of it is shown (all of a card that is
// taller than the conversation cannot be, so 70% of the smaller of the two counts).
export function cardView(card: Span, box: Span): "in" | "above" | "below" {
  const h = card.bottom - card.top;
  const boxH = box.bottom - box.top;
  if (h <= 0 || boxH <= 0) return "in";
  const shown = Math.max(0, Math.min(card.bottom, box.bottom) - Math.max(card.top, box.top));
  if (shown >= Math.min(h, boxH) * 0.7) return "in";
  return card.top < box.top ? "above" : "below";
}

export function waitingText(brief: string): string {
  return "Waiting for you: " + brief;
}

// "Show ↑" or "Show ↓" by where the card is; "Show" for something that is not in the conversation (a change in the editor).
export function showLabel(side: "in" | "above" | "below" | null): string {
  return side === "above" ? "Show ↑" : side === "below" ? "Show ↓" : "Show";
}

// The text of the arrow to the last message: "Latest" with the number of messages that came while the user was reading.
export function jumpLabel(newCount: number): string {
  return newCount > 0 ? "Latest · " + newCount + " new" : "";
}
