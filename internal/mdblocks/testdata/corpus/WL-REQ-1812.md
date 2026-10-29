
A form has unsaved changes when its current values differ from the values it
opened with. Changing a field and changing it back does not count.

Leaving such a form shows a dialog, "You have unsaved changes.", with three
buttons. Focus starts on Keep editing.

- **Save & leave** submits. If the server answers 422, the dialog closes, the
  navigation is cancelled and the errors show under their fields. Any other
  error keeps the dialog open with an error line. On success the draft is
  deleted and the navigation continues.
- **Discard** deletes the draft and continues the navigation.
- **Keep editing**, the X, Escape and a click on the backdrop close only the
  dialog. Escape over a slide-in panel does not close the panel too.

Leaving means an in-app navigation (links, header nav, Back, Forward,
through TanStack Router's `useBlocker`), closing a slide-in panel, or the
form's own close button. A refresh or a closed tab gets no dialog. The
browser allows only its own there, and the draft survives both. One hook,
`useLeaveGuard`, gives every form this behavior.
