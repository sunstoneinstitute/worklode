// Roomier sizing for form fields (the New/Edit Idea form, the pitch editor),
// layered on top of the shadcn field components via className. Kept out of
// the base components so the compact search inputs in table pickers and
// filters (which also use Input / InputGroup) stay small.
export const FORM_INPUT = "h-11 px-4"
export const FORM_TEXTAREA = "px-4 py-3"
export const FORM_SELECT_TRIGGER = "w-full py-2.5 pr-3 pl-4 data-[size=default]:h-11"
// InputGroup's own height, plus its input's padding
export const FORM_INPUT_GROUP = "h-11"
export const FORM_INPUT_GROUP_INPUT = "px-4"
