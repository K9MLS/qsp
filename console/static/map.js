/* QSP peer map.
 *
 * A slippy map with no library. Leaflet is a fine piece of work and most of it
 * is features this does not need; what a map of a club's hotspots requires is
 * Web Mercator arithmetic, a grid of image elements, and a drag handler. See
 * docs/adr/ADR-0025-no-bundled-map.md.
 *
 * Keeping it here rather than vendoring keeps the console what it is: markup,
 * stylesheets and scripts with no build step, servable from a LAN with no route
 * out — where, with the tile URL cleared, the pins still draw on nothing.
 */
(function () {
  "use strict";

  var TILE = 256;

  /* Web Mercator. The projection every slippy map uses, in the only two
   * functions anybody needs from it: a longitude is linear in x, and a latitude
   * is not. */
  function lonToX(lon, zoom) {
    return ((lon + 180) / 360) * Math.pow(2, zoom) * TILE;
  }

  function latToY(lat, zoom) {
    /* Clamped to the latitudes Mercator can represent. The projection sends
     * the poles to infinity, and a station at 89° would otherwise be placed
     * some enormous distance off the canvas rather than near the top of it. */
    var clamped = Math.max(-85.05112878, Math.min(85.05112878, lat));
    var rad = (clamped * Math.PI) / 180;
    var y = (1 - Math.log(Math.tan(rad) + 1 / Math.cos(rad)) / Math.PI) / 2;
    return y * Math.pow(2, zoom) * TILE;
  }

  function xToLon(x, zoom) {
    return (x / (Math.pow(2, zoom) * TILE)) * 360 - 180;
  }

  function yToLat(y, zoom) {
    var n = Math.PI - (2 * Math.PI * y) / (Math.pow(2, zoom) * TILE);
    return (180 / Math.PI) * Math.atan(0.5 * (Math.exp(n) - Math.exp(-n)));
  }

  /* fit chooses a centre and zoom that show every point with a margin.
   *
   * A single peer gets a sensible neighbourhood view rather than the maximum
   * zoom, because a club with one hotspot looking at its own roof learns
   * nothing. */
  function fit(points, width, height, maxZoom) {
    if (!points.length) {
      return null;
    }
    var lats = points.map(function (p) { return p.lat; });
    var lons = points.map(function (p) { return p.lon; });
    var centre = {
      lat: (Math.max.apply(null, lats) + Math.min.apply(null, lats)) / 2,
      lon: (Math.max.apply(null, lons) + Math.min.apply(null, lons)) / 2
    };

    if (points.length === 1) {
      return { centre: centre, zoom: Math.min(11, maxZoom) };
    }

    for (var zoom = maxZoom; zoom > 1; zoom--) {
      var xs = lons.map(function (lon) { return lonToX(lon, zoom); });
      var ys = lats.map(function (lat) { return latToY(lat, zoom); });
      var w = Math.max.apply(null, xs) - Math.min.apply(null, xs);
      var h = Math.max.apply(null, ys) - Math.min.apply(null, ys);
      /* 0.8 leaves the outermost pins off the edge, where they can be seen
       * whole rather than clipped in half by the frame. */
      if (w < width * 0.8 && h < height * 0.8) {
        return { centre: centre, zoom: zoom };
      }
    }
    return { centre: centre, zoom: 2 };
  }

  /* Map draws tiles and markers into an element and lets them be dragged. */
  function Map(container, settings) {
    this.el = container;
    this.settings = settings || {};
    this.maxZoom = this.settings.max_zoom || 18;
    this.centre = { lat: 0, lon: 0 };
    this.zoom = 2;
    this.points = [];
    /* Whether the view has been fitted to the points. A resize redraws and
     * does not refit: somebody who has panned away from their club should not
     * be yanked back because the window changed width. */
    this.fitted = false;

    this.el.classList.add("map");
    this.el.innerHTML =
      '<div class="map__canvas"></div>' +
      '<div class="map__controls">' +
      '<button type="button" class="map__zoom" data-zoom="1" aria-label="Zoom in">+</button>' +
      '<button type="button" class="map__zoom" data-zoom="-1" aria-label="Zoom out">\u2212</button>' +
      "</div>" +
      (this.settings.attribution
        ? '<div class="map__attribution"></div>'
        : "");

    this.canvas = this.el.querySelector(".map__canvas");

    /* Attribution is set as text rather than markup. It is operator-supplied
     * configuration, and configuration is not a reason to trust a string with
     * innerHTML. */
    var credit = this.el.querySelector(".map__attribution");
    if (credit) {
      credit.textContent = this.settings.attribution;
    }

    this.bindControls();
    this.bindDrag();
    this.bindResize();
  }

  /* bindResize redraws when the element's size changes.
   *
   * It covers the case that broke this — a panel measured before it was laid
   * out — and the ordinary one of somebody resizing the window, which
   * previously left the tiles covering the old width. */
  Map.prototype.bindResize = function () {
    var self = this;
    if (typeof ResizeObserver !== "function") {
      /* Old browsers get a window listener, which handles the resize case and
       * not the late-layout one. Better than nothing and not worth more. */
      window.addEventListener("resize", function () { self.refit(); });
      return;
    }
    this.observer = new ResizeObserver(function () { self.refit(); });
    this.observer.observe(this.el);
  };

  Map.prototype.bindControls = function () {
    var self = this;
    var buttons = this.el.querySelectorAll(".map__zoom");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].addEventListener("click", function (event) {
        var by = parseInt(event.currentTarget.getAttribute("data-zoom"), 10);
        self.zoom = Math.max(1, Math.min(self.maxZoom, self.zoom + by));
        self.draw();
      });
    }
  };

  Map.prototype.bindDrag = function () {
    var self = this;
    var dragging = false;
    var last = null;

    this.canvas.addEventListener("pointerdown", function (event) {
      dragging = true;
      last = { x: event.clientX, y: event.clientY };
      self.canvas.setPointerCapture(event.pointerId);
      self.el.classList.add("map--dragging");
    });

    this.canvas.addEventListener("pointermove", function (event) {
      if (!dragging) {
        return;
      }
      var dx = event.clientX - last.x;
      var dy = event.clientY - last.y;
      last = { x: event.clientX, y: event.clientY };

      /* Panning moves the centre, not the tiles. Translating the canvas would
       * be smoother and would drift out of step with the coordinates the
       * markers are placed from, which is how a pin ends up beside its town
       * rather than on it. */
      var cx = lonToX(self.centre.lon, self.zoom) - dx;
      var cy = latToY(self.centre.lat, self.zoom) - dy;
      self.centre = { lon: xToLon(cx, self.zoom), lat: yToLat(cy, self.zoom) };
      self.draw();
    });

    function stop(event) {
      if (!dragging) {
        return;
      }
      dragging = false;
      self.el.classList.remove("map--dragging");
      if (event.pointerId !== undefined && self.canvas.hasPointerCapture(event.pointerId)) {
        self.canvas.releasePointerCapture(event.pointerId);
      }
    }
    this.canvas.addEventListener("pointerup", stop);
    this.canvas.addEventListener("pointercancel", stop);
  };

  /* size returns the element's own box, or null when it has not got one yet.
   *
   * **Measuring once and keeping the answer was the bug.** A panel that is not
   * laid out when show() runs measures zero, the code fell back to 600x320
   * inside a frame nearly three times that wide, and every tile and pin landed
   * relative to a viewport that did not exist — which is what put the only pin
   * in the top-left corner instead of the middle.
   *
   * clientWidth rather than getBoundingClientRect, because it is the box the
   * absolutely-positioned children are placed inside and so is the number the
   * arithmetic actually needs. */
  Map.prototype.size = function () {
    var width = this.el.clientWidth;
    var height = this.el.clientHeight;
    if (width < 1 || height < 1) {
      return null;
    }
    return { width: width, height: height };
  };

  /* show replaces the points and refits the view. */
  Map.prototype.show = function (points) {
    this.points = points || [];
    this.fitted = false;
    this.refit();
  };

  /* refit recomputes the view and redraws, if the element has a size yet.
   *
   * Called from show and from the resize observer, so a map created inside a
   * panel that is not laid out yet draws correctly the moment it is — rather
   * than drawing wrongly and staying that way. */
  Map.prototype.refit = function () {
    var box = this.size();
    if (!box) {
      return;
    }
    if (!this.fitted) {
      var view = fit(this.points, box.width, box.height, this.maxZoom);
      if (view) {
        this.centre = view.centre;
        this.zoom = view.zoom;
      }
      this.fitted = true;
    }
    this.draw();
  };

  Map.prototype.draw = function () {
    var box = this.size();
    if (!box) {
      /* Nothing is drawn rather than something drawn wrongly. The observer
       * calls back when there is a size, and an empty frame for a moment is
       * better than a pin in the wrong place for ever. */
      return;
    }
    var width = box.width;
    var height = box.height;

    var centreX = lonToX(this.centre.lon, this.zoom);
    var centreY = latToY(this.centre.lat, this.zoom);
    var originX = centreX - width / 2;
    var originY = centreY - height / 2;

    var html = "";
    if (this.settings.tile_url) {
      html += this.tiles(originX, originY, width, height);
    }
    html += this.markers(originX, originY);
    this.canvas.innerHTML = html;
  };

  Map.prototype.tiles = function (originX, originY, width, height) {
    var scale = Math.pow(2, this.zoom);
    var firstX = Math.floor(originX / TILE);
    var firstY = Math.floor(originY / TILE);
    var lastX = Math.floor((originX + width) / TILE);
    var lastY = Math.floor((originY + height) / TILE);

    var html = "";
    for (var ty = firstY; ty <= lastY; ty++) {
      /* Above the north edge or below the south there is no tile. Requesting
       * one fetches a 404 per pan, which is rude to a donated server and
       * pointless everywhere else. */
      if (ty < 0 || ty >= scale) {
        continue;
      }
      for (var tx = firstX; tx <= lastX; tx++) {
        /* Longitude wraps, so a tile column off one edge is a real tile from
         * the other. Without this the map goes blank when dragged past the
         * date line. */
        var wrapped = ((tx % scale) + scale) % scale;
        var url = this.settings.tile_url
          .replace("{z}", this.zoom)
          .replace("{x}", wrapped)
          .replace("{y}", ty);
        html +=
          '<img class="map__tile" alt="" aria-hidden="true" loading="lazy" src="' +
          escapeAttribute(url) +
          '" style="left:' + (tx * TILE - originX) + "px;top:" +
          (ty * TILE - originY) + 'px">';
      }
    }
    return html;
  };

  Map.prototype.markers = function (originX, originY) {
    var html = "";
    for (var i = 0; i < this.points.length; i++) {
      var p = this.points[i];
      var x = lonToX(p.lon, this.zoom) - originX;
      var y = latToY(p.lat, this.zoom) - originY;
      /* Pins outside the frame are skipped. One drawn at a negative offset
       * escaped the panel's overflow in the corner case and overlapped the
       * header above it. */
      if (x < -TILE || y < -TILE || x > this.el.clientWidth + TILE ||
          y > this.el.clientHeight + TILE) {
        continue;
      }
      html +=
        '<div class="map__pin" style="left:' + x + "px;top:" + y + 'px">' +
        '<span class="map__pin-dot" aria-hidden="true"></span>' +
        '<span class="map__pin-label">' + escapeAttribute(p.label) + "</span>" +
        "</div>";
    }
    return html;
  };

  /* Escaping here rather than reusing the console's helper keeps this file
   * standalone: it is the one piece that could be lifted out, and a shared
   * function would make that a lie. */
  function escapeAttribute(value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  window.QSPMap = { Map: Map, fit: fit, lonToX: lonToX, latToY: latToY, xToLon: xToLon, yToLat: yToLat };
})();
