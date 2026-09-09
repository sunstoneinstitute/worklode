// ching.js rings the top bar's worklode mark when you click it: a short bell,
// a semitone higher for each click in a streak, and a fanfare plus a spin on
// every tenth. Synthesised with WebAudio rather than shipped as an audio file,
// so there is no asset to license and nothing to fetch. Only the mark itself
// swallows the click; the "worklode" wordmark beside it, and keyboard
// activation of the link, still go home.
// Served at /assets/ching.js by internal/api's assetHandler; a static asset,
// not a generated artifact, so it carries no drift-check surface.
(function () {
  var mark = document.querySelector(".brand .mark");
  var Ctor = window.AudioContext || window.webkitAudioContext;
  if (!mark || !Ctor) return;

  var C6 = 1046.5;
  var audio, streak = 0, last = 0;

  // One bell strike at `freq`. The three partials at 1:2:3.01 and the near
  // instant attack are what make it read as a "ching" and not a beep.
  function ring(freq, at, dur, gain) {
    [1, 2, 3.01].forEach(function (partial, i) {
      var osc = audio.createOscillator();
      var amp = audio.createGain();
      osc.type = i ? "sine" : "triangle";
      osc.frequency.value = freq * partial;
      amp.gain.setValueAtTime(0, at);
      amp.gain.linearRampToValueAtTime(gain / (i + 1.6), at + 0.004);
      amp.gain.exponentialRampToValueAtTime(0.0001, at + dur / (i + 1));
      osc.connect(amp).connect(audio.destination);
      osc.start(at);
      osc.stop(at + dur);
    });
  }

  mark.addEventListener("click", function (e) {
    e.preventDefault();
    audio = audio || new Ctor();
    if (audio.state === "suspended") audio.resume();

    if (Date.now() - last > 2000) streak = 0;
    last = Date.now();
    streak++;

    ring(C6 * Math.pow(2, Math.min(streak - 1, 12) / 12), audio.currentTime, 0.5, 0.22);

    if (streak % 10 === 0) {
      [0, 4, 7, 12].forEach(function (semitones, i) {
        ring(C6 * Math.pow(2, semitones / 12), audio.currentTime + 0.12 + i * 0.09, 0.8, 0.25);
      });
      mark.classList.remove("struck");
      void mark.offsetWidth; // restart the animation on a tenth-click streak
      mark.classList.add("struck");
    }
  });

  mark.addEventListener("animationend", function () {
    mark.classList.remove("struck");
  });
})();
