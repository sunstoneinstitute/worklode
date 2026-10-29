
Actions sit on the right of an expanded row and on plan lines. A button the
viewer cannot use is rendered disabled with the reason in its hover text, never
hidden. **Every write is a two-step button**: the first click turns it, in
place and at the same size, into a confirmation naming the act in full with
Confirm and Cancel; Cancel, a click elsewhere or Escape restores it. While in
flight it shows busy and ignores clicks. The page never applies the result
itself: the server records the change, the event reaches the page through the
stream, and the row re-renders. A failure restores the button and shows the
server's reason inline. The confirm step protects against mis-clicks only;
WL-REQ-1338 is the CSRF protection.

