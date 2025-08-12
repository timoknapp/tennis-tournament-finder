(function enableMobileOneFingerZoom(map) {
  const isTouchCapable = (('ontouchstart' in window) ||
                          navigator.maxTouchPoints > 0 ||
                          navigator.msMaxTouchPoints > 0);
  if (!isTouchCapable || !map) return;

  const container = map.getContainer();

  // Use the same transform pipeline Leaflet uses for pinch if available
  const canUseInternalZoomAnim = !!(map._zoomAnimated && typeof map._animateZoom === 'function');

  let lastTapTime = 0;
  const doubleTapThreshold = 300; // ms

  let holdZoomActive = false;
  let movedEnoughForDrag = false;

  let startY = 0;
  let startZoom = 0;
  let startCenter = null;

  let desiredZoom = 0;

  let anchorLatLng = null;
  let anchorContainerPt = null;

  let rafPending = false;
  const dragActivationThreshold = 10; // px
  const sensitivity = 0.01; // tune to taste (higher => faster zoom)

  function onTouchStart(e) {
    if (e.touches.length !== 1) return;

    const now = Date.now();
    const elapsed = now - lastTapTime;

    if (elapsed < doubleTapThreshold) {
      // Second tap detected -> enter hold-to-zoom mode
      const t = e.touches[0];
      const pseudoMouseEvt = { clientX: t.clientX, clientY: t.clientY };

      anchorContainerPt = map.mouseEventToContainerPoint(pseudoMouseEvt);
      anchorLatLng = map.containerPointToLatLng(anchorContainerPt);

      holdZoomActive = true;
      movedEnoughForDrag = false;

      startY = t.clientY;
      startZoom = map.getZoom();
      startCenter = map.getCenter();
      desiredZoom = startZoom;

      // Stop any ongoing animations; disable dragging during the gesture
      map.stop();
      map.dragging.disable();

      ensureRAF();
      e.preventDefault();
    } else {
      lastTapTime = now;
    }
  }

  function onTouchMove(e) {
    if (!holdZoomActive || e.touches.length !== 1) return;

    const dy = e.touches[0].clientY - startY;
    if (Math.abs(dy) > dragActivationThreshold) movedEnoughForDrag = true;

    desiredZoom = clamp(startZoom + dy * sensitivity, map.getMinZoom(), map.getMaxZoom());

    ensureRAF();
    e.preventDefault();
  }

  function onTouchEnd(e) {
    if (!holdZoomActive) return;

    // If the user actually dragged, commit to the final zoom/center (no animation)
    if (movedEnoughForDrag) {
      const finalState = computeZoomState(anchorLatLng, anchorContainerPt, desiredZoom);
      map.setView(finalState.center, finalState.zoom, { animate: false });
    } else {
      // If it was just a quick double-tap, do the classic zoom-in with animation
      if (anchorLatLng && typeof map.setZoomAround === 'function') {
        map.setZoomAround(anchorLatLng, clamp(map.getZoom() + 1, map.getMinZoom(), map.getMaxZoom()));
      } else {
        map.zoomIn(1);
      }
    }

    // Re-enable dragging
    map.dragging.enable();

    reset();
    e.preventDefault();
  }

  function onTouchCancel() {
    if (holdZoomActive) map.dragging.enable();
    reset();
  }

  function reset() {
    holdZoomActive = false;
    movedEnoughForDrag = false;

    startY = 0;
    startZoom = 0;
    startCenter = null;

    desiredZoom = 0;

    anchorLatLng = null;
    anchorContainerPt = null;

    rafPending = false;
  }

  function ensureRAF() {
    if (!rafPending) {
      rafPending = true;
      requestAnimationFrame(tick);
    }
  }

  function tick() {
    rafPending = false;
    if (!holdZoomActive) return;

    const state = computeZoomState(anchorLatLng, anchorContainerPt, desiredZoom);

    if (canUseInternalZoomAnim) {
      // IMPORTANT: pass zoom (not scale). This mirrors Leaflet's TouchZoom handler.
      try {
        map._animateZoom(state.center, state.zoom, anchorContainerPt);
      } catch (_) {
        // Fallback if Leaflet internals differ
        setZoomAroundNoAnim(anchorLatLng, state.zoom);
      }
    } else {
      // Fallback: immediate zoom + pan to keep anchor; not as smooth as true anim
      setZoomAroundNoAnim(anchorLatLng, state.zoom);
    }

    if (holdZoomActive) ensureRAF();
  }

  // Compute the center needed so the given anchor latlng stays under the same container point at 'zoom'
  function computeZoomState(anchorLL, anchorPt, zoom) {
    const size = map.getSize();
    // World pixel at target zoom for anchor
    const p1 = map.project(anchorLL, zoom);
    // Desired center in world pixels: C1 = P1 - (a - size/2)
    const centerPoint = p1.subtract(anchorPt.subtract(size.divideBy(2)));
    const centerLatLng = map.unproject(centerPoint, zoom);
    return { center: centerLatLng, zoom };
  }

  function setZoomAroundNoAnim(latlng, zoom) {
    if (!latlng) {
      map.setZoom(zoom, { animate: false });
      return;
    }
    // Keep the anchor fixed by panning after a non-animated zoom
    const before = map.latLngToContainerPoint(latlng);
    map.setZoom(zoom, { animate: false });
    const after = map.latLngToContainerPoint(latlng);
    const offset = after.subtract(before);
    map.panBy(offset, { animate: false });
  }

  function clamp(v, min, max) {
    return Math.max(min, Math.min(max, v));
  }

  container.addEventListener('touchstart', onTouchStart, { passive: false });
  container.addEventListener('touchmove', onTouchMove, { passive: false });
  container.addEventListener('touchend', onTouchEnd, { passive: false });
  container.addEventListener('touchcancel', onTouchCancel, { passive: false });
})(window.map);