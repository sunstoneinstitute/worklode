import worklodeMark from "@/assets/brand/worklode-mark.svg"

// A single, always-mounted logo mark that lives outside Header and
// ProjectToolbar's own DOM (position: fixed), so it can visibly travel
// between the two instead of two separate elements cross-fading. Header's
// own inline mark is hidden (via its `hideLogoMark` prop) wherever this is
// used, so there's never a duplicate. Position/size for both states are
// driven entirely by CSS (see [data-scroll-logo] in index.css), toggled by
// the header-hidden class useHeaderScroll sets.
export function ScrollLogo() {
  return <img data-scroll-logo src={worklodeMark} alt="" className="hidden lg:block" />
}
