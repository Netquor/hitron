/* Visual effects only: aurora backdrop, cursor spotlight, scroll reveal, nav
   state. Nothing here is needed to read or use the page. */
(function () {
  'use strict';
  var root = document.documentElement;
  var reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // Backdrop layers
  var aurora = document.createElement('div');
  aurora.className = 'aurora';
  aurora.setAttribute('aria-hidden', 'true');
  aurora.innerHTML = '<i></i><i></i><i></i>';
  var grain = document.createElement('div');
  grain.className = 'grain';
  grain.setAttribute('aria-hidden', 'true');
  document.body.prepend(aurora);
  document.body.appendChild(grain);

  // Cursor spotlight on glass panels
  var SPOT = '.card, .cli-steps li, .chart-card, .steps > li, .stats > div, .hero-card, .log-card, .install, .keys';
  document.addEventListener('pointermove', function (e) {
    var el = e.target.closest && e.target.closest(SPOT);
    if (!el) return;
    var r = el.getBoundingClientRect();
    el.style.setProperty('--mx', (e.clientX - r.left) + 'px');
    el.style.setProperty('--my', (e.clientY - r.top) + 'px');
  }, { passive: true });

  // Nav: elevated after scrolling, highlight the section in view
  var bar = document.querySelector('.topbar');
  var links = [].slice.call(document.querySelectorAll('.topnav a[href^="#"]'));
  function onScroll() { if (bar) bar.classList.toggle('scrolled', window.scrollY > 12); }
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });

  if ('IntersectionObserver' in window) {
    var map = {};
    links.forEach(function (a) { map[a.getAttribute('href').slice(1)] = a; });
    var spy = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (!en.isIntersecting || !map[en.target.id]) return;
        links.forEach(function (a) { a.classList.remove('active'); });
        map[en.target.id].classList.add('active');
      });
    }, { rootMargin: '-35% 0px -55% 0px' });
    Object.keys(map).forEach(function (id) {
      var s = document.getElementById(id);
      if (s) spy.observe(s);
    });

    // Scroll reveal, staggered within a group
    if (!reduce) {
      var groups = ['.section-head', '.stats > div', '.cli-hero > *', '.cli-steps li', '.cli-feature', '.keys',
        '.steps > li', '.cards > *', '.chart-card', '.tutorial', '.tabs, .tab-body', '.install', '.local-alt',
        '.console-bar', '.explorer', '.response-panel', '.log-card', '.log-toolbar', '.marquee'];
      var seen = [];
      groups.forEach(function (sel) {
        [].slice.call(document.querySelectorAll(sel)).forEach(function (el, i) {
          if (seen.indexOf(el) !== -1) return;
          seen.push(el);
          el.setAttribute('data-reveal', '');
          el.style.setProperty('--d', Math.min(i, 5) * 70 + 'ms');
        });
      });
      root.classList.add('fx');
      var io = new IntersectionObserver(function (entries) {
        entries.forEach(function (en) {
          if (en.isIntersecting) { en.target.classList.add('in'); io.unobserve(en.target); }
        });
      }, { rootMargin: '0px 0px -8% 0px', threshold: 0.08 });
      seen.forEach(function (el) { io.observe(el); });
    }
  }
})();
