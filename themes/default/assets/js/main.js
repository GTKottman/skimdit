/* Skimdit default theme. Every feature here is an enhancement: pages work without it. */
(function () {
  "use strict";
  var doc = document, root = doc.documentElement;

  // Light/dark toggle (the page follows the OS until the reader picks).
  var toggle = doc.querySelector(".theme-toggle");
  if (toggle) {
    toggle.hidden = false;
    var current = function () {
      var t = root.getAttribute("data-theme");
      if (t) return t;
      return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
    };
    var sync = function () { toggle.setAttribute("aria-pressed", current() === "light" ? "true" : "false"); };
    sync();
    toggle.addEventListener("click", function () {
      var next = current() === "light" ? "dark" : "light";
      root.setAttribute("data-theme", next);
      try { localStorage.setItem("skimdit-theme", next); } catch (e) {}
      sync();
    });
  }

  // Copy buttons go in each code block's header band.
  if (navigator.clipboard) {
    doc.querySelectorAll("figure.code").forEach(function (fig) {
      var pre = fig.querySelector("pre"), head = fig.querySelector(".code-head");
      if (!pre || !head) return;
      var b = doc.createElement("button");
      b.type = "button";
      b.className = "copy-code";
      b.textContent = "Copy";
      b.addEventListener("click", function () {
        navigator.clipboard.writeText(pre.innerText).then(function () {
          b.textContent = "Copied";
          setTimeout(function () { b.textContent = "Copy"; }, 1500);
        });
      });
      head.appendChild(b);
    });
  }

  // Click-to-load video embeds: nothing is fetched from YouTube until the reader asks.
  doc.querySelectorAll("a.embed-facade[data-embed=youtube]").forEach(function (a) {
    a.addEventListener("click", function (ev) {
      ev.preventDefault();
      var id = a.getAttribute("data-id"), start = a.getAttribute("data-start");
      if (!/^[A-Za-z0-9_-]{6,20}$/.test(id)) return;
      var f = doc.createElement("iframe");
      f.src = "https://www.youtube-nocookie.com/embed/" + id + "?autoplay=1" + (/^\d+$/.test(start || "") ? "&start=" + start : "");
      f.title = a.querySelector(".embed-title").textContent;
      f.allow = "autoplay; encrypted-media; picture-in-picture; fullscreen";
      f.setAttribute("allowfullscreen", "");
      f.loading = "lazy";
      a.replaceWith(f);
      f.focus();
    });
  });

  // Footnote popovers: hovering or focusing a reference shows the note.
  doc.querySelectorAll("a.footnote-ref").forEach(function (a) {
    var id = decodeURIComponent((a.getAttribute("href") || "").slice(1));
    var note = doc.getElementById(id);
    if (!note) return;
    var tip;
    var show = function () {
      if (tip) return;
      tip = doc.createElement("span");
      tip.className = "footnote-pop";
      tip.setAttribute("role", "tooltip");
      tip.innerHTML = note.innerHTML;
      tip.querySelectorAll(".footnote-backref").forEach(function (b) { b.remove(); });
      a.parentNode.appendChild(tip);
    };
    var hide = function () { if (tip) { tip.remove(); tip = null; } };
    a.addEventListener("mouseenter", show);
    a.addEventListener("focus", show);
    a.addEventListener("mouseleave", hide);
    a.addEventListener("blur", hide);
  });

  // Image lightbox: click an image in a post to see it full size.
  doc.querySelectorAll(".entry-content figure img").forEach(function (img) {
    img.style.cursor = "zoom-in";
    img.addEventListener("click", function () {
      var d = doc.createElement("dialog");
      d.className = "lightbox";
      var big = doc.createElement("img");
      big.src = img.currentSrc || img.src;
      big.alt = img.alt;
      d.appendChild(big);
      d.addEventListener("click", function () { d.close(); });
      d.addEventListener("close", function () { d.remove(); });
      doc.body.appendChild(d);
      d.showModal();
    });
  });

  // Search (the index loads only when the search box is used).
  var form = doc.querySelector(".search-form[data-index]");
  if (form) {
    var input = form.querySelector("input[type=search]");
    var list = doc.querySelector(".search-results");
    var status = doc.querySelector(".search-status");
    var strings = doc.getElementById("search-strings");
    var index = null, loading = null;
    var load = function () {
      if (!loading) {
        loading = fetch(form.getAttribute("data-index")).then(function (r) { return r.json(); }).then(function (m) {
          return Promise.all(m.chunks.map(function (c) { return fetch(c).then(function (r) { return r.json(); }); }));
        }).then(function (parts) {
          index = { entries: [].concat.apply([], parts.map(function (p) { return p.entries; })) };
        });
      }
      return loading;
    };
    var words = function (s) { return s.toLowerCase().normalize("NFKD").replace(/[̀-ͯ]/g, "").split(/[^a-z0-9]+/).filter(Boolean); };
    var run = function () {
      var q = input.value.trim();
      if (!q) { list.innerHTML = ""; status.textContent = ""; return; }
      load().then(function () {
        var terms = words(q);
        var hits = index.entries.map(function (e) {
          var score = 0;
          terms.forEach(function (t) {
            if (e.t.indexOf(t) >= 0) score += 10;
            if (e.g.indexOf(t) >= 0) score += 6;
            if (e.x.indexOf(t) >= 0) score += 3;
            if (e.b && e.b.indexOf(t) >= 0) score += 1;
          });
          return { e: e, s: score };
        }).filter(function (h) { return h.s > 0; }).sort(function (a, b) { return b.s - a.s; }).slice(0, 30);
        list.innerHTML = "";
        hits.forEach(function (h) {
          var li = doc.createElement("li");
          li.className = "entry-item";
          var a = doc.createElement("a");
          a.className = "entry-item-title";
          a.href = h.e.u;
          a.textContent = h.e.title;
          var time = doc.createElement("time");
          time.className = "entry-item-date";
          time.textContent = h.e.d;
          var p = doc.createElement("p");
          p.className = "entry-item-excerpt";
          p.textContent = h.e.excerpt;
          li.append(a, time, p);
          list.appendChild(li);
        });
        status.textContent = hits.length === 1 ? strings.dataset.one : hits.length ? strings.dataset.count.replace("{n}", hits.length) : strings.dataset.none.replace("{q}", q);
      });
    };
    var timer;
    input.addEventListener("input", function () { clearTimeout(timer); timer = setTimeout(run, 120); });
    input.addEventListener("focus", load, { once: true });
    form.addEventListener("submit", function (ev) { ev.preventDefault(); run(); history.replaceState(null, "", "?q=" + encodeURIComponent(input.value)); });
    var q = new URLSearchParams(location.search).get("q");
    if (q) { input.value = q; run(); }
  }

  // Reply buttons fill in which comment a new comment answers.
  doc.querySelectorAll("[data-reply]").forEach(function (b) {
    b.hidden = false;
    b.addEventListener("click", function () {
      var f = doc.querySelector(".comment-form");
      if (!f) return;
      f.querySelector("input[name=parent]").value = b.getAttribute("data-reply");
      var note = f.querySelector(".replying-to");
      note.textContent = "\u21b3 " + b.getAttribute("data-author");
      note.hidden = false;
      f.querySelector("textarea").focus();
    });
  });

  // Comment form proof-of-work: find a nonce whose SHA-256 has N leading zero bits.
  doc.querySelectorAll("form[data-pow]").forEach(function (f) {
    f.addEventListener("submit", function (ev) {
      var pow = f.querySelector("input[name=pow]");
      if (pow.value) return;
      ev.preventDefault();
      var btn = f.querySelector("button[type=submit]");
      btn.disabled = true;
      fetch(f.action.replace(/comments$/, "pow")).then(function (r) { return r.json(); }).then(function (c) {
        var enc = new TextEncoder(), n = 0;
        var step = function () {
          var batch = [];
          for (var i = 0; i < 200; i++, n++) batch.push(n);
          Promise.all(batch.map(function (k) {
            return crypto.subtle.digest("SHA-256", enc.encode(c.challenge + ":" + k)).then(function (h) { return [k, new Uint8Array(h)]; });
          })).then(function (rs) {
            for (var j = 0; j < rs.length; j++) {
              if (zeroBits(rs[j][1]) >= c.bits) { pow.value = c.challenge + ":" + rs[j][0]; f.submit(); return; }
            }
            step();
          });
        };
        step();
      }).catch(function () { btn.disabled = false; });
    });
  });
  function zeroBits(b) {
    var n = 0;
    for (var i = 0; i < b.length; i++) {
      if (b[i] === 0) { n += 8; continue; }
      for (var m = 128; m && !(b[i] & m); m >>= 1) n++;
      return n;
    }
    return n;
  }
})();
