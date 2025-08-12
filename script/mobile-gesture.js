(function enableMobileOneFingerZoom(map) {
  const isTouchCapable = (('ontouchstart' in window) ||
                          navigator.maxTouchPoints > 0 ||
                          navigator.msMaxTouchPoints > 0);
  if (!isTouchCapable || !map) return;

  const container = map.getContainer();

  let lastTapTime = 0;
  const doubleTapThreshold = 300; // ms
  let secondTapLatLng = null;

  let holdZoomActive = false;
  let startY = 0;
  let startZoom = 0;

  // Easing + state for smoother, continuous zooming
  let desiredZoom = 0;
  let easedZoom = 0;
  let appliedZoom = 0;
  const easeAlpha = 0.35;   // higher => faster easing
  const applyEpsilon = 0.001;

  let rafPending = false;
  let latestDY = 0;

  const dragActivationThreshold = 10; // px
  let movedEnoughForDrag = false;

  function onTouchStart(e) {
    if (e.touches.length !== 1) return;
    const now = Date.now();
    const elapsed = now - lastTapTime;

    if (elapsed < doubleTapThreshold) {
      // Second tap detected; start hold-to-zoom mode
      const t = e.touches[0];
      secondTapLatLng = map.mouseEventToLatLng({ clientX: t.clientX, clientY: t.clientY });
      holdZoomActive = true;
      startY = t.clientY;
      startZoom = map.getZoom();
      desiredZoom = startZoom;
      easedZoom = startZoom;
      appliedZoom = startZoom;
      movedEnoughForDrag = false;
      map.dragging.disable();
      e.preventDefault();
      ensureRAF();
    } else {
      lastTapTime = now;
    }
  }

  function onTouchMove(e) {
    if (!holdZoomActive || e.touches.length !== 1) return;
    const dy = e.touches[0].clientY - startY;
    if (Math.abs(dy) > dragActivationThreshold) movedEnoughForDrag = true;
    latestDY = dy;

    // Convert vertical drag to a desired zoom level
    const sensitivity = 0.01; // slightly lower for finer control
    desiredZoom = clamp(startZoom + latestDY * sensitivity, map.getMinZoom(), map.getMaxZoom());

    ensureRAF();
    e.preventDefault();
  }

  function tick() {
    rafPending = false;
    if (!holdZoomActive) return;

    // Ease towards desired zoom for continuous, non-steppy feel
    easedZoom += (desiredZoom - easedZoom) * easeAlpha;

    if (Math.abs(easedZoom - appliedZoom) > applyEpsilon) {
      if (secondTapLatLng) {
        map.setZoomAround(secondTapLatLng, easedZoom);
      } else {
        map.setZoom(easedZoom);
      }
      appliedZoom = easedZoom;
    }

    // Continue the loop while active or while we still have easing to settle
    if (holdZoomActive || Math.abs(desiredZoom - easedZoom) > applyEpsilon) {
      ensureRAF();
    }
  }

  function onTouchEnd(e) {
    if (!holdZoomActive) return;

    // Finish easing to the last desired zoom
    desiredZoom = clamp(desiredZoom, map.getMinZoom(), map.getMaxZoom());
    easedZoom = desiredZoom;
    if (Math.abs(easedZoom - appliedZoom) > applyEpsilon) {
      if (secondTapLatLng) {
        map.setZoomAround(secondTapLatLng, easedZoom);
      } else {
        map.setZoom(easedZoom);
      }
      appliedZoom = easedZoom;
    }

    map.dragging.enable();

    // If user didn’t drag far enough, treat as quick double-tap zoom-in
    if (!movedEnoughForDrag) {
      if (secondTapLatLng) {
        map.setZoomAround(secondTapLatLng, map.getZoom() + 1);
      } else {
        map.zoomIn(1);
      }
    }

    reset();
    e.preventDefault();
  }

  function onTouchCancel() {
    if (holdZoomActive) map.dragging.enable();
    reset();
  }

  function reset() {
    holdZoomActive = false;
    secondTapLatLng = null;
    rafPending = false;
  }

  function ensureRAF() {
    if (!rafPending) {
      rafPending = true;
      requestAnimationFrame(tick);
    }
  }

  function clamp(v, min, max) {
    return Math.max(min, Math.min(max, v));
  }

  container.addEventListener('touchstart', onTouchStart, { passive: false });
  container.addEventListener('touchmove', onTouchMove, { passive: false });
  container.addEventListener('touchend', onTouchEnd, { passive: false });
  container.addEventListener('touchcancel', onTouchCancel, { passive: false });
})(window.map);