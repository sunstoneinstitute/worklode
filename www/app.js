// Reveal sections on scroll. Progressive enhancement only — everything is
// already readable with JS disabled (see the .reveal fallback in styles.css
// for prefers-reduced-motion).
(function () {
  var targets = document.querySelectorAll('.reveal');

  if (!('IntersectionObserver' in window)) {
    targets.forEach(function (el) { el.classList.add('in'); });
    return;
  }

  var observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (!entry.isIntersecting) return;
      entry.target.classList.add('in');
      observer.unobserve(entry.target);
    });
  }, { rootMargin: '0px 0px -10% 0px', threshold: 0.08 });

  targets.forEach(function (el, i) {
    el.style.transitionDelay = (i % 4) * 60 + 'ms';
    observer.observe(el);
  });
})();
