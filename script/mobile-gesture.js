(function enableMobileOneFingerZoom(map) {
  const isTouchCapable = (("ontouchstart" in window) ||
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
      movedEnoughForDrag = false;
      map.dragging.disable();
      e.preventDefault();
    } else {
      lastTapTime = now;
    }
  }

  function onTouchMove(e) {
    if (!holdZoomActive || e.touches.length !== 1) return;
    const dy = e.touches[0].clientY - startY;
    if (Math.abs(dy) > dragActivationThreshold) movedEnoughForDrag = true;
    latestDY = dy;

    if (!rafPending) {
      rafPending = true;
      requestAnimationFrame(applyZoomDrag);
    }
    e.preventDefault();
  }

  function applyZoomDrag() {
    rafPending = false;
    if (!holdZoomActive) return;

    const sensitivity = 0.015; // tune for feel
    let targetZoom = startZoom + latestDY * sensitivity;
    targetZoom = Math.max(map.getMinZoom(), Math.min(map.getMaxZoom(), targetZoom));

    if (secondTapLatLng) {
      map.setZoomAround(secondTapLatLng, targetZoom);
    } else {
      map.setZoom(targetZoom);
    }
  }

  function onTouchEnd(e) {
    if (!holdZoomActive) return;
    map.dragging.enable();

    // If user didn't drag far enough, treat as quick double-tap zoom-in
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

  container.addEventListener('touchstart', onTouchStart, { passive: false });
  container.addEventListener('touchmove', onTouchMove, { passive: false });
  container.addEventListener('touchend', onTouchEnd, { passive: false });
  container.addEventListener('touchcancel', onTouchCancel, { passive: false });
})(window.map);