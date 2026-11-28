(function () {
  'use strict';

  const channel = 'i3-prototype-annotations';
  const parentOrigin = new URL(location.href).origin;
  const svgNS = 'http://www.w3.org/2000/svg';
  if (parent === window || parentOrigin === 'null') return;

  let route = currentRoute();
  let layer;
  let drawing = false;
  let annotations = [];
  let gesture;
  let cursor;
  let previousFocus;
  let scheduled = 0;
  let suppressClick = false;

  function currentRoute() {
    const hash = location.hash.split('?', 1)[0];
    return hash.startsWith('#/') ? hash : '';
  }

  function send(type, values = {}) {
    parent.postMessage({ channel, type, route, ...values }, parentOrigin);
  }

  function svg(name) {
    return document.createElementNS(svgNS, name);
  }

  function viewport() {
    return {
      left: 0, top: 0,
      right: document.documentElement.clientWidth,
      bottom: document.documentElement.clientHeight,
    };
  }

  function clamp(value, min, max) {
    return Math.min(max, Math.max(min, value));
  }

  function bodyPath(element) {
    const indices = [];
    for (let node = element; node !== document.body; node = node.parentElement) {
      const owner = node.parentElement;
      if (!owner) return null;
      indices.unshift(Array.prototype.indexOf.call(owner.children, node));
    }
    const path = indices.join('/');
    return path.length <= 256 ? path : null;
  }

  function resolveAnchor(path) {
    if (!path) return document.scrollingElement;
    let element = document.body;
    for (const index of path.split('/')) {
      element = element?.children[Number(index)];
      if (!element) return null;
    }
    return element;
  }

  function canScroll(element) {
    const style = getComputedStyle(element);
    const allows = (overflow) => /^(auto|scroll|overlay)$/.test(overflow);
    return (allows(style.overflowX) && element.scrollWidth > element.clientWidth) ||
      (allows(style.overflowY) && element.scrollHeight > element.clientHeight);
  }

  function anchorAt(x, y) {
    const first = document.elementsFromPoint(x, y).find((element) => !layer.contains(element));
    for (let element = first; element && element !== document.body; element = element.parentElement) {
      if (canScroll(element)) {
        const path = bodyPath(element);
        if (path !== null) return path;
      }
    }
    return '';
  }

  function boxFor(element) {
    const rect = element.getBoundingClientRect();
    const scaleX = rect.width / element.offsetWidth || 1;
    const scaleY = rect.height / element.offsetHeight || 1;
    const left = rect.left + element.clientLeft * scaleX;
    const top = rect.top + element.clientTop * scaleY;
    return {
      left, top, scaleX, scaleY,
      right: left + element.clientWidth * scaleX,
      bottom: top + element.clientHeight * scaleY,
    };
  }

  function metrics(anchor) {
    const element = resolveAnchor(anchor);
    if (!element) return null;
    const visible = viewport();
    const root = !anchor;
    const box = root ? { left: 0, top: 0, scaleX: 1, scaleY: 1 } : boxFor(element);
    const width = element.scrollWidth;
    const height = element.scrollHeight;
    if (!width || !height || !(box.scaleX > 0) || !(box.scaleY > 0)) return null;

    if (!root) {
      if (!element.clientWidth || !element.clientHeight) return null;
      // 内层滚动区也会被外层的滚动区裁切，不能把框画到侧栏或滚动区外。
      for (let node = element; node && node !== document.documentElement; node = node.parentElement) {
        const style = getComputedStyle(node);
        const clip = node === element ? box : boxFor(node);
        if (node === element || style.overflowX !== 'visible') {
          visible.left = Math.max(visible.left, clip.left);
          visible.right = Math.min(visible.right, clip.right);
        }
        if (node === element || style.overflowY !== 'visible') {
          visible.top = Math.max(visible.top, clip.top);
          visible.bottom = Math.min(visible.bottom, clip.bottom);
        }
      }
    }
    const rtl = getComputedStyle(element).direction === 'rtl';
    const scrollX = element.scrollLeft + (rtl ? width - element.clientWidth : 0);
    return {
      visible, width, height, scaleX: box.scaleX, scaleY: box.scaleY,
      x: box.left - scrollX * box.scaleX,
      y: box.top - element.scrollTop * box.scaleY,
    };
  }

  function pointAt(area, clientX, clientY) {
    const { visible } = area;
    if (visible.right <= visible.left || visible.bottom <= visible.top) return null;
    const x = clamp(clientX, visible.left, visible.right);
    const y = clamp(clientY, visible.top, visible.bottom);
    return {
      x: clamp((x - area.x) / area.scaleX / area.width, 0, 1),
      y: clamp((y - area.y) / area.scaleY / area.height, 0, 1),
    };
  }

  function geometry(start, end) {
    return {
      x: Math.min(start.x, end.x),
      y: Math.min(start.y, end.y),
      w: Math.abs(end.x - start.x),
      h: Math.abs(end.y - start.y),
    };
  }

  function validAnnotation(mark) {
    if (!mark || !mark.preview || typeof mark.preview.route !== 'string' ||
        typeof mark.preview.anchor !== 'string' || mark.preview.anchor.length > 256 ||
        !/^(?:\d+(?:\/\d+)*)?$/.test(mark.preview.anchor)) return false;
    const { x, y, w, h } = mark;
    return [x, y, w, h].every(Number.isFinite) && x >= 0 && y >= 0 && w > 0 && h > 0 &&
      x + w <= 1.000001 && y + h <= 1.000001;
  }

  function appendMark(fragment, mark, anchor, draft) {
    const area = metrics(anchor);
    if (!area) return;
    const { left, top, right, bottom } = area.visible;
    const width = mark.w * area.width * area.scaleX;
    const height = mark.h * area.height * area.scaleY;
    const x = area.x + mark.x * area.width * area.scaleX;
    const y = area.y + mark.y * area.height * area.scaleY;
    if (right <= left || bottom <= top || x + width < left || x > right || y + height < top || y > bottom) return;

    // 先裁切再画原边框，滚出视口的边不会变成滚动区边缘的一条新线。
    const clip = svg('svg');
    clip.setAttribute('x', left);
    clip.setAttribute('y', top);
    clip.setAttribute('width', right - left);
    clip.setAttribute('height', bottom - top);
    clip.style.setProperty('width', (right - left) + 'px', 'important');
    clip.style.setProperty('height', (bottom - top) + 'px', 'important');
    clip.style.setProperty('overflow', 'hidden', 'important');
    clip.style.setProperty('pointer-events', 'none', 'important');
    const rect = svg('rect');
    rect.setAttribute('x', x - left);
    rect.setAttribute('y', y - top);
    rect.setAttribute('width', width);
    rect.setAttribute('height', height);
    rect.setAttribute('data-testid', 'prototype-native-annotation-box');
    if (draft) rect.setAttribute('data-draft', 'true');
    rect.style.setProperty('fill', draft ? '#1b75e820' : '#ff9a2424', 'important');
    rect.style.setProperty('stroke', draft ? '#1b75e8' : '#b34c00', 'important');
    rect.style.setProperty('stroke-width', '2px', 'important');
    rect.style.setProperty('pointer-events', 'none', 'important');
    if (draft) rect.style.setProperty('stroke-dasharray', '5 3', 'important');
    clip.append(rect);
    fragment.append(clip);
  }

  function draw() {
    scheduled = 0;
    checkRoute();
    if (!layer) return;
    const fragment = document.createDocumentFragment();
    for (const mark of annotations) {
      if (mark.preview.route === route) appendMark(fragment, mark, mark.preview.anchor, mark.draft === true);
    }
    if (gesture?.end) appendMark(fragment, geometry(gesture.start, gesture.end), gesture.anchor, true);
    if (drawing && cursor) {
      const cross = svg('path');
      const { x, y } = cursor;
      cross.setAttribute('d', 'M ' + (x - 7) + ' ' + y + ' h 14 M ' + x + ' ' + (y - 7) + ' v 14');
      cross.style.setProperty('stroke', '#1b75e8', 'important');
      cross.style.setProperty('stroke-width', '2px', 'important');
      cross.style.setProperty('pointer-events', 'none', 'important');
      fragment.append(cross);
    }
    layer.replaceChildren(fragment);
  }

  function schedule() {
    if (layer && !scheduled) scheduled = requestAnimationFrame(draw);
  }

  function setDrawing(next) {
    if (drawing === next) return;
    drawing = next;
    if (!next) {
      const pointer = gesture?.pointer;
      if (pointer !== undefined && layer?.hasPointerCapture(pointer)) layer.releasePointerCapture(pointer);
      gesture = undefined;
      cursor = undefined;
      if (layer && document.activeElement === layer && previousFocus?.isConnected) {
        previousFocus.focus?.({ preventScroll: true });
      }
      previousFocus = undefined;
    } else if (layer) {
      previousFocus = document.activeElement;
      layer.focus({ preventScroll: true });
    }
    if (layer) layer.style.setProperty('pointer-events', drawing ? 'auto' : 'none', 'important');
    schedule();
  }

  function cancel() {
    setDrawing(false);
    send('cancel');
  }

  function checkRoute() {
    const next = currentRoute();
    if (next === route) return false;
    const interrupted = drawing || annotations.some((mark) => mark.draft && mark.preview.route === route);
    route = next;
    setDrawing(false);
    send('route');
    if (interrupted) send('cancel');
    schedule();
    return true;
  }

  function startGesture(x, y, pointer) {
    const anchor = anchorAt(x, y);
    const area = metrics(anchor);
    const point = area && pointAt(area, x, y);
    if (point) gesture = { anchor, start: point, end: point, pointer };
  }

  function endGesture(x, y) {
    const active = gesture;
    if (!active) return;
    const area = metrics(active.anchor);
    const end = area && pointAt(area, x, y);
    const box = end && geometry(active.start, end);
    if (!box || box.w * area.width * area.scaleX < 4 || box.h * area.height * area.scaleY < 4) {
      gesture = undefined;
      schedule();
      return;
    }
    setDrawing(false);
    send('box', { ...box, anchor: active.anchor });
  }

  function consume(event) {
    event.preventDefault();
    event.stopImmediatePropagation();
  }

  function armPointer(event) {
    if (checkRoute() || !drawing) return;
    consume(event);
    if (!event.isPrimary || event.button !== 0) return;
    suppressClick = true;
    cursor = undefined;
    startGesture(event.clientX, event.clientY, event.pointerId);
    if (gesture) layer.setPointerCapture(event.pointerId);
    schedule();
  }

  function movePointer(event) {
    if (!drawing || gesture?.pointer !== event.pointerId) return;
    consume(event);
    const area = metrics(gesture.anchor);
    if (area) gesture.end = pointAt(area, event.clientX, event.clientY);
    schedule();
  }

  function releasePointer(event) {
    if (!drawing) return;
    consume(event);
    if (gesture?.pointer === event.pointerId) endGesture(event.clientX, event.clientY);
    if (event.isPrimary) setTimeout(() => { suppressClick = false; }, 0);
  }

  function key(event) {
    if ((!drawing && event.key !== 'Escape') || checkRoute()) return;
    if (event.key === 'Escape') {
      if (drawing || annotations.some((mark) => mark.draft && mark.preview.route === route)) {
        consume(event);
        cancel();
      }
      return;
    }
    if (!['ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown', 'Enter'].includes(event.key)) return;
    consume(event);
    const frame = viewport();
    if (!cursor) cursor = { x: frame.right / 2, y: frame.bottom / 2 };
    if (event.key === 'Enter') {
      if (event.repeat) return;
      if (gesture) endGesture(cursor.x, cursor.y);
      else startGesture(cursor.x, cursor.y);
    } else {
      const step = event.shiftKey ? 2 : 12;
      if (event.key === 'ArrowLeft') cursor.x -= step;
      if (event.key === 'ArrowRight') cursor.x += step;
      if (event.key === 'ArrowUp') cursor.y -= step;
      if (event.key === 'ArrowDown') cursor.y += step;
      cursor.x = clamp(cursor.x, 0, frame.right - 1);
      cursor.y = clamp(cursor.y, 0, frame.bottom - 1);
      if (gesture && gesture.pointer === undefined) {
        const area = metrics(gesture.anchor);
        if (area) gesture.end = pointAt(area, cursor.x, cursor.y);
      }
    }
    schedule();
  }

  window.addEventListener('message', (event) => {
    const data = event.data;
    if (event.source !== parent || event.origin !== parentOrigin || data?.channel !== channel ||
        data.type !== 'state' || typeof data.drawing !== 'boolean' || !Array.isArray(data.annotations)) return;
    const changed = checkRoute();
    annotations = data.annotations.filter(validAnnotation);
    if (!changed || !data.drawing) setDrawing(data.drawing);
    schedule();
  });

  function start() {
    layer = svg('svg');
    layer.setAttribute('data-testid', 'prototype-native-annotation-layer');
    layer.setAttribute('tabindex', '-1');
    layer.setAttribute('aria-label', '框选批注。方向键移动，回车标记两个角，Esc 取消。');
    layer.style.cssText = 'position:fixed!important;inset:0!important;width:100%!important;height:100%!important;' +
      'z-index:2147483647!important;display:block!important;outline:none!important;touch-action:none!important;' +
      'overflow:hidden!important;cursor:crosshair!important;pointer-events:none!important;margin:0!important;padding:0!important;';
    // 保持原型 body.children 的路径，路由卸载节点时也不会删掉批注层。
    document.documentElement.append(layer);
    if (drawing) {
      drawing = false;
      setDrawing(true);
    }
    window.addEventListener('pointerdown', armPointer, true);
    window.addEventListener('pointermove', movePointer, true);
    window.addEventListener('pointerup', releasePointer, true);
    window.addEventListener('pointercancel', () => {
      if (drawing) cancel();
      suppressClick = false;
    }, true);
    window.addEventListener('click', (event) => {
      if (drawing || suppressClick) consume(event);
    }, true);
    window.addEventListener('contextmenu', (event) => { if (drawing) consume(event); }, true);
    window.addEventListener('keydown', key, true);
    window.addEventListener('scroll', schedule, { capture: true, passive: true });
    window.addEventListener('resize', schedule, { passive: true });
    window.addEventListener('hashchange', checkRoute);
    window.addEventListener('popstate', checkRoute);
    window.addEventListener('load', schedule, true);
    new MutationObserver(schedule).observe(document.body, {
      childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style', 'hidden'],
    });
    // history.pushState 不触发 hashchange，保留 SPA 路由器自己的 history 实现。
    setInterval(checkRoute, 150);
    checkRoute();
    send('ready');
    schedule();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true });
  else start();
})();
