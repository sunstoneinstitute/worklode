import { useEffect, useRef, useState } from "react"
import { useBlocker } from "@tanstack/react-router"

/**
 * Holds every way out of a form with unsaved changes, so the form can ask
 * first (WL-REQ-1812): in-app navigation (links, Back, Forward) through the
 * router's blocker, and the form's own close through requestClose. A
 * refresh or closing the tab isn't held: the browser only allows its own
 * dialog there, and the draft survives both.
 */
export function useLeaveGuard({ dirty, onClose }: { dirty: boolean; onClose: () => void }) {
  // Once the form is saved or discarded, the exit that follows goes through.
  // It stays set because every caller leaves the form after that.
  const released = useRef(false)
  // The blocker asks at navigation time, after this render
  const dirtyRef = useRef(dirty)
  useEffect(() => {
    dirtyRef.current = dirty
  }, [dirty])
  const blocker = useBlocker({
    shouldBlockFn: () => dirtyRef.current && !released.current,
    withResolver: true,
    enableBeforeUnload: false,
  })
  const [closing, setClosing] = useState(false)

  return {
    /** An exit is waiting for the person to choose */
    held: blocker.status === "blocked" || closing,
    /** Close, or ask first if there are unsaved changes */
    requestClose: () => {
      if (dirty && !released.current) setClosing(true)
      else onClose()
    },
    /** Let the next exit through without asking (after a successful save) */
    release: () => {
      released.current = true
    },
    /** Go where the person was going: on with the held navigation, or close */
    proceed: () => {
      released.current = true
      if (blocker.status === "blocked") blocker.proceed()
      else {
        setClosing(false)
        onClose()
      }
    },
    /** Stay on the form */
    stay: () => {
      if (blocker.status === "blocked") blocker.reset()
      setClosing(false)
    },
  }
}
