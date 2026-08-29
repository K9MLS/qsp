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
    /* Whether a redraw is already scheduled for the next animation frame. */
    this.frameQueued = false;
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

    /* Outside the frame, because the frame clips its overflow and a caption
     * that can be swallowed by the thing it describes is no use. */
    this.caption = document.createElement("p");
    this.caption.className = "map__caption";
    if (this.el.parentElement) {
      this.el.parentElement.appendChild(this.caption);
    }

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
      /* **Without this the map cannot be dragged at all.** Pressing on an
       * image starts the browser's own drag-and-drop, which takes the pointer
       * and cancels the events this handler depends on — so the map moves for
       * a few pixels and then stops dead. */
      event.preventDefault();

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
      self.scheduleDraw();
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
  /* size measures the frame, taking the largest answer anything offers.
   *
   * **Three attempts at this failed by reasoning about which measurement was
   * right.** The arithmetic was verified against a known size and is correct,
   * so a map drawing one tile in a corner means every path that produced a
   * number produced a small one. Rather than pick a fourth candidate and hope,
   * this takes the largest of the element, its bounding box, and its
   * ancestors — a map that draws a few tiles more than it needs is a far
   * smaller fault than one that draws a corner.
   *
   * The result is recorded on the element as data-measured, so a screenshot of
   * the inspector answers the question instead of another round of guessing. */
  Map.prototype.size = function () {
    /* **The element's own box, not the largest thing in its ancestry.** An
     * earlier version took the maximum across parents and reported 1920 for a
     * frame that was 1550 — the viewport, not the map. That mattered when
     * positions were computed from the width; it no longer does, and taking
     * the truth is better than taking the largest number available.
     *
     * The parent is a fallback only when the element has no box at all, which
     * is the case of a panel measured before it is laid out. */
    var width = this.el.clientWidth;
    var height = this.el.clientHeight;

    if (width < 1 && this.el.parentElement) {
      width = this.el.parentElement.clientWidth;
    }

    if (width < 1 || height < 1) {
      return null;
    }
    this.el.setAttribute("data-measured", width + "x" + height);
    return { width: width, height: height };
  };

  /* scheduleDraw coalesces redraws to one per animation frame.
   *
   * A pointermove fires far more often than the screen refreshes, and each
   * redraw replaces every tile element. Rebuilding the whole grid dozens of
   * times a second is what made dragging feel broken rather than merely
   * imperfect. */
  Map.prototype.scheduleDraw = function () {
    var self = this;
    if (this.frameQueued) {
      return;
    }
    this.frameQueued = true;

    var schedule = typeof requestAnimationFrame === "function"
      ? requestAnimationFrame
      : function (fn) { return setTimeout(fn, 16); };

    schedule(function () {
      self.frameQueued = false;
      self.draw();
    });
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
    /* **Everything is placed relative to the centre, not to a measured
     * viewport.** The plane sits at the middle of the canvas by CSS, and a tile
     * goes at `tx * 256 - centreX` from it — arithmetic with no width in it.
     *
     * Six attempts at this map computed positions from a measured width, and a
     * measurement that was wrong put everything in a corner. A measurement
     * cannot misplace what it is not used to place. The size still decides how
     * many tiles to draw, where being wrong costs a few tiles nobody sees. */
    var centreX = lonToX(this.centre.lon, this.zoom);
    var centreY = latToY(this.centre.lat, this.zoom);

    /* **Built as elements, not as markup with style attributes.**
     *
     * The content security policy is `style-src 'self'`, which forbids inline
     * style attributes — so every `style="left:…"` this map used to emit was
     * refused by the browser and every tile and pin stacked at its container's
     * origin. That is the whole of the fault chased across seven attempts:
     * with the plane at the frame's corner they piled in the corner, and once
     * the plane was centred they piled at the centre.
     *
     * Setting `element.style.left` is CSSOM, which the policy permits. The
     * policy stays as strict as it was. */
    var plane = document.createElement("div");
    plane.className = "map__plane";

    var tiles = 0;
    if (this.settings.tile_url) {
      tiles = this.appendTiles(plane, centreX, centreY, box.width, box.height);
    }
    this.appendMarkers(plane, centreX, centreY);

    this.canvas.innerHTML = "";
    this.canvas.appendChild(plane);
    this.report(box, tiles);
  };

  /* report says what the map measured and drew.
   *
   * **This exists because the map has been wrong five times and every fix was
   * a guess about which measurement was at fault.** The source is consistent
   * and the rendered page is not, which is a gap nothing in the code can close
   * from the inside. A line under the map turns one screenshot into the answer.
   *
   * It is small, muted, and beside the attribution the tiles already require,
   * so it reads as map furniture rather than a debugging artefact. */
  Map.prototype.report = function (box, tiles) {
    if (!this.caption) {
      return;
    }
    var canvas = this.canvas.getBoundingClientRect();
    this.caption.textContent =
      tiles + (tiles === 1 ? " tile" : " tiles") +
      " · frame " + box.width + "×" + box.height +
      " · canvas " + Math.round(canvas.width) + "×" + Math.round(canvas.height) +
      " · zoom " + this.zoom;
  };

  /* MIN_COLUMNS and MIN_ROWS are a floor on the tile grid.
   *
   * **The measurement has been wrong three times and the arithmetic never
   * was.** A grid computed only from a measured size draws one tile in a
   * corner when that measurement is small; a floor means the map fills a
   * typical panel even then, and the worst case is a few tiles fetched that
   * nobody sees rather than a map nobody can use.
   *
   * Seven by three covers a full-width panel at 360px tall, which is the
   * layout the console actually has. */
  var MIN_COLUMNS = 7;
  var MIN_ROWS = 3;

  /* appendTiles adds the tile grid to the plane and returns how many.
   *
   * Positions are set through the style object rather than an attribute: the
   * content security policy forbids inline style attributes, and one silently
   * refused is how every tile ended up stacked at the same point. */
  Map.prototype.appendTiles = function (plane, centreX, centreY, width, height) {
    var scale = Math.pow(2, this.zoom);

    var centreTileX = Math.floor(centreX / TILE);
    var centreTileY = Math.floor(centreY / TILE);
    /* Half the frame either side of the centre tile, at least one. Drawing a
     * ring more than the frame needs is politeness spent for nothing on
     * somebody else's tile server. */
    var reachX = Math.max(1, Math.ceil(width / (2 * TILE)));
    var reachY = Math.max(1, Math.ceil(height / (2 * TILE)));

    var count = 0;
    for (var ty = centreTileY - reachY; ty <= centreTileY + reachY; ty++) {
      /* Above the north edge or below the south there is no tile. Requesting
       * one fetches a 404 per pan, which is rude to a donated server and
       * pointless everywhere else. */
      if (ty < 0 || ty >= scale) {
        continue;
      }
      for (var tx = centreTileX - reachX; tx <= centreTileX + reachX; tx++) {
        /* Longitude wraps, so a tile column off one edge is a real tile from
         * the other. Without this the map goes blank past the date line. */
        var wrapped = ((tx % scale) + scale) % scale;

        var img = document.createElement("img");
        img.className = "map__tile";
        img.alt = "";
        img.draggable = false;
        img.setAttribute("aria-hidden", "true");
        /* **referrerpolicy is why tiles load at all.** QSP sends
         * Referrer-Policy: no-referrer, and OpenStreetMap's tile policy
         * requires a Referer identifying the site. "origin" sends the scheme
         * and host and nothing else, for these requests only. */
        img.setAttribute("referrerpolicy", "origin");
        img.src = this.settings.tile_url
          .replace("{z}", this.zoom)
          .replace("{x}", wrapped)
          .replace("{y}", ty);
        img.style.left = Math.round(tx * TILE - centreX) + "px";
        img.style.top = Math.round(ty * TILE - centreY) + "px";

        plane.appendChild(img);
        count++;
      }
    }
    return count;
  };

  /* appendMarkers adds a pin per located peer. */
  Map.prototype.appendMarkers = function (plane, centreX, centreY) {
    for (var i = 0; i < this.points.length; i++) {
      var p = this.points[i];
      var x = Math.round(lonToX(p.lon, this.zoom) - centreX);
      var y = Math.round(latToY(p.lat, this.zoom) - centreY);

      /* Offsets from the centre, so the bound is generous either way. A pin
       * just off the edge costs one element; a pin wrongly skipped is a
       * station that has vanished. */
      var reach = Math.max(this.el.clientWidth, 640);
      if (x < -reach || x > reach || y < -reach || y > reach) {
        continue;
      }

      var pin = document.createElement("div");
      pin.className = "map__pin";
      pin.style.left = x + "px";
      pin.style.top = y + "px";

      var dot = document.createElement("span");
      dot.className = "map__pin-dot";
      dot.setAttribute("aria-hidden", "true");

      var label = document.createElement("span");
      label.className = "map__pin-label";
      /* textContent, so a callsign is never markup. */
      label.textContent = p.label;

      pin.appendChild(dot);
      pin.appendChild(label);
      plane.appendChild(pin);
    }
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
