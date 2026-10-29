
A named task cell pulses once (scale and glow, about a second); a
named row highlights its left edge; `prefers-reduced-motion` makes both an
instant color change. Animation changes transform and color only.

Layout stability: band, counts, bar and footer have fixed heights; a row swap
keeps height when section and plan counts are unchanged and otherwise grows
only below the pointer's row; a new spec appears at the end of its group; a
spec changing group moves only when the pointer is off the list; tooltips
never push content.

