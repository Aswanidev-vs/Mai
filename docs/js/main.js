/* ========================================
   Mai — Landing Page Interactions
   Minimal, intentional. No framework needed.
   ======================================== */

(function () {
  'use strict';

  // --- Nav scroll effect ---
  const nav = document.getElementById('nav');

  function onScroll() {
    if (window.scrollY > 40) {
      nav.classList.add('scrolled');
    } else {
      nav.classList.remove('scrolled');
    }
  }

  window.addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  // --- Mobile toggle ---
  const mobileToggle = document.getElementById('mobileToggle');
  const navLinks = document.querySelector('.nav-links');
  let mobileOpen = false;

  if (mobileToggle && navLinks) {
    mobileToggle.addEventListener('click', function () {
      mobileOpen = !mobileOpen;
      if (mobileOpen) {
        navLinks.style.display = 'flex';
        navLinks.style.flexDirection = 'column';
        navLinks.style.position = 'absolute';
        navLinks.style.top = '64px';
        navLinks.style.left = '0';
        navLinks.style.right = '0';
        navLinks.style.background = 'rgba(12, 11, 16, 0.95)';
        navLinks.style.backdropFilter = 'blur(16px)';
        navLinks.style.padding = '24px';
        navLinks.style.gap = '20px';
        navLinks.style.borderBottom = '1px solid var(--n-800)';
        navLinks.style.opacity = '0';
        navLinks.style.transform = 'translateY(-8px)';
        navLinks.style.transition = 'opacity 0.4s cubic-bezier(0.22, 1, 0.36, 1), transform 0.4s cubic-bezier(0.22, 1, 0.36, 1)';
        requestAnimationFrame(function () {
          navLinks.style.opacity = '1';
          navLinks.style.transform = 'translateY(0)';
        });
      } else {
        navLinks.style.opacity = '0';
        navLinks.style.transform = 'translateY(-8px)';
        setTimeout(function () {
          navLinks.removeAttribute('style');
        }, 400);
      }
    });
  }

  // --- Scroll reveal ---
  function setupReveal() {
    var els = document.querySelectorAll(
      '.feature-card, .pipeline-step, .compare-row, .cta-inner, .code-block, .section-header'
    );

    els.forEach(function (el) {
      el.classList.add('reveal');
    });

    if (!('IntersectionObserver' in window)) {
      els.forEach(function (el) { el.classList.add('visible'); });
      return;
    }

    var observer = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) {
            var parent = entry.target.parentElement;
            var siblings = Array.from(parent.children).filter(function (c) {
              return c.classList.contains('reveal');
            });
            var idx = siblings.indexOf(entry.target);
            var delay = idx * 80;

            setTimeout(function () {
              entry.target.classList.add('visible');
            }, delay);

            observer.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.08, rootMargin: '0px 0px -60px 0px' }
    );

    els.forEach(function (el) {
      observer.observe(el);
    });
  }

  // --- Smooth anchor scroll ---
  document.querySelectorAll('a[href^="#"]').forEach(function (a) {
    a.addEventListener('click', function (e) {
      var id = a.getAttribute('href');
      if (id === '#') return;
      var target = document.querySelector(id);
      if (target) {
        e.preventDefault();
        target.scrollIntoView({ behavior: 'smooth', block: 'start' });

        if (mobileOpen) {
          mobileOpen = false;
          navLinks.removeAttribute('style');
        }
      }
    });
  });

  // --- Init ---
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', setupReveal);
  } else {
    setupReveal();
  }
})();
