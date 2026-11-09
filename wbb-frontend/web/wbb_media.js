// Browser media resources for Flutter Web. Credentials and signaling stay in
// ApiClient; this module owns devices, WebAudio, WebRTC and HLS player objects.
(() => {
  const publishers = new Map();
  const viewers = new Map();
  let nextId = 0;
  const id = () => String(++nextId);
  const stopped = a => a.closed || a.displayVideo?.readyState === 'ended';
  const stopTracks = stream => stream?.getTracks().forEach(t => { if (t.readyState !== 'ended') t.stop(); });

  function disposeAttempt(a) {
    a.closed = true;
    stopTracks(a.display);
    stopTracks(a.mic);
    stopTracks(a.mixed);
    stopTracks(a.dest?.stream);
    try { a.pc?.close(); } catch (_) {}
    if (a.preview.srcObject) a.preview.srcObject = null;
    const closing = a.audio && a.audio.state !== 'closed' ? a.audio.close() : Promise.resolve();
    return Promise.resolve(closing).catch(() => {});
  }

  function stopPublisher(key) {
    const a = publishers.get(key);
    if (!a) return Promise.resolve();
    publishers.delete(key);
    return disposeAttempt(a);
  }

  async function resumeAudio(key) {
    const a = publishers.get(key);
    if (!a || a.closed || !a.audio) return false;
    try {
      await Promise.race([a.audio.resume(), new Promise(resolve => setTimeout(resolve, 1500))]);
    } catch (_) {}
    return a.audio.state === 'running';
  }

  async function waitForIce(pc) {
    if (pc.iceGatheringState === 'complete') return;
    await new Promise(resolve => {
      const finish = () => { clearTimeout(timer); pc.removeEventListener('icegatheringstatechange', check); resolve(); };
      const check = () => { if (pc.iceGatheringState === 'complete' || pc.signalingState === 'closed') finish(); };
      const timer = setTimeout(finish, 2500);
      pc.addEventListener('icegatheringstatechange', check);
    });
  }

  async function captureOffer(key) {
    const a = publishers.get(key);
    if (!a || a.closed) throw new Error('capture cancelled');
    // Must be the first asynchronous browser operation after the click.
    try {
      const display = await navigator.mediaDevices.getDisplayMedia({
        video: { frameRate: 30 }, audio: {}, selfBrowserSurface: 'exclude',
      });
      a.display = display; // register originals before any other await
      const video = display.getVideoTracks()[0];
      a.displayVideo = video;
      if (!video) throw new Error('screen video is required');
      video.addEventListener('ended', () => { if (!a.closed) a.onEnded(); }, { once: true });
      if (stopped(a)) throw new Error('capture cancelled');
      const mic = await navigator.mediaDevices.getUserMedia({ audio: true });
      a.mic = mic;
      if (stopped(a)) throw new Error('capture cancelled');
      if (!mic.getAudioTracks().length) throw new Error('microphone is required');
      const audio = new AudioContext();
      a.audio = audio;
      const dest = audio.createMediaStreamDestination();
      a.dest = dest;
      const shared = display.getAudioTracks();
      if (shared.length) {
        const gain = audio.createGain(); gain.gain.value = 0.5;
        audio.createMediaStreamSource(new MediaStream(shared)).connect(gain).connect(dest);
      }
      const micGain = audio.createGain(); micGain.gain.value = 0.5;
      audio.createMediaStreamSource(mic).connect(micGain).connect(dest);
      // resume may need another user gesture on some browsers; the UI reports
      // its observed state rather than claiming shared audio is audible.
      try {
        await Promise.race([audio.resume(), new Promise(resolve => setTimeout(resolve, 1500))]);
      } catch (_) {}
      if (stopped(a)) throw new Error('capture cancelled');
      const mixed = new MediaStream([video, ...dest.stream.getAudioTracks()]);
      a.mixed = mixed;
      a.preview.srcObject = mixed;
      a.preview.play().catch(() => {});
      const pc = new RTCPeerConnection();
      a.pc = pc;
      const capabilities = RTCRtpSender.getCapabilities('video');
      const h264 = capabilities?.codecs?.filter(c => c.mimeType.toLowerCase() === 'video/h264') || [];
      if (!h264.length) throw new Error('H.264 publishing is unavailable');
      const videoSender = pc.addTransceiver(video, { direction: 'sendonly' });
      videoSender.setCodecPreferences(h264);
      pc.addTransceiver(dest.stream.getAudioTracks()[0], { direction: 'sendonly' });
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      await waitForIce(pc);
      if (stopped(a)) throw new Error('capture cancelled');
      const sdp = pc.localDescription?.sdp || '';
      if (!sdp.includes('H264/90000') || !sdp.includes('m=audio') || !sdp.includes('m=video')) {
        throw new Error('H.264/audio negotiation failed');
      }
      return JSON.stringify({ sdp, sharedAudio: shared.length > 0, audioRunning: audio.state === 'running' });
    } catch (error) {
      if (publishers.get(key) === a) publishers.delete(key);
      // A permission prompt can resolve after stopPublisher removed the map
      // entry. Always release the exact attempt object and its late tracks.
      await disposeAttempt(a);
      throw error;
    }
  }

  function newPublisher(onEnded) {
    const key = id();
    const preview = document.createElement('video');
    preview.muted = true; preview.autoplay = true; preview.playsInline = true;
    preview.setAttribute('playsinline', '');
    preview.style.cssText = 'width:100%;max-height:300px;background:#111';
    publishers.set(key, { closed: false, preview, onEnded, display: null, mic: null,
      mixed: null, dest: null, audio: null, pc: null, displayVideo: null });
    return key;
  }

  async function acceptAnswer(key, sdp) {
    const a = publishers.get(key);
    if (!a || a.closed || !a.pc) throw new Error('publish attempt ended');
    const videoSection = sdp.split(/\r?\nm=video /)[1]?.split(/\r?\nm=/)[0];
    const mediaLine = videoSection?.split(/\r?\n/, 1)[0]?.trim().split(/\s+/) || [];
    const payloads = new Set(mediaLine.slice(2));
    const h264 = [...videoSection?.matchAll(/(?:^|\r?\n)a=rtpmap:(\d+) H264\/90000(?:\r?\n|$)/gi) || []]
      .some(match => payloads.has(match[1]));
    if (!videoSection || mediaLine[0] === '0' || !h264) {
      throw new Error('H.264 was not negotiated');
    }
    await a.pc.setRemoteDescription({ type: 'answer', sdp });
    if (stopped(a)) throw new Error('publish attempt ended');
  }

  function newViewer(url, onEvent) {
    const key = id();
    const video = document.createElement('video');
    video.controls = true; video.muted = true; video.playsInline = true;
    video.setAttribute('playsinline', '');
    video.style.cssText = 'width:100%;max-height:420px;background:#111';
    const v = { url, video, hls: null, closed: false, userPaused: false,
      internalPause: false, fallback: false, mode: '', onEvent };
    video.addEventListener('pause', () => { if (!v.internalPause) v.userPaused = true; });
    video.addEventListener('error', () => {
      if (v.closed) return;
      if (v.mode === 'native' && !v.fallback && window.Hls?.isSupported()) {
        v.fallback = true;
        v.internalPause = true; video.pause(); v.internalPause = false;
        video.removeAttribute('src'); video.load();
        v.mode = '';
        attach(v);
        if (!v.userPaused) video.play().catch(() => onEvent('autoplay-blocked'));
      } else onEvent('playback-error');
    });
    viewers.set(key, v);
    return key;
  }

  function attach(v) {
    if (v.hls || v.mode) return;
    if (window.Hls?.isSupported()) {
      const hls = new window.Hls({ lowLatencyMode: true, backBufferLength: 30 });
      v.hls = hls; v.mode = 'mse';
      hls.on(window.Hls.Events.ERROR, (_, data) => {
        if (!v.closed && data.fatal) {
          hls.destroy(); v.hls = null; v.mode = '';
          v.onEvent('playback-error');
        }
      });
      hls.loadSource(v.url); hls.attachMedia(v.video);
    } else if (v.video.canPlayType('application/vnd.apple.mpegurl')) {
      v.mode = 'native'; v.video.src = v.url;
    } else throw new Error('HLS playback is unavailable');
  }

  async function playViewer(key, withSound) {
    const v = viewers.get(key);
    if (!v || v.closed) throw new Error('player ended');
    attach(v);
    if (withSound) v.video.muted = false;
    v.userPaused = false;
    try { await v.video.play(); }
    catch (error) { v.onEvent('autoplay-blocked'); throw error; }
  }

  function goLive(key) {
    const v = viewers.get(key);
    if (!v || v.closed) return;
    const position = v.hls?.liveSyncPosition ??
      (v.video.seekable.length ? v.video.seekable.end(v.video.seekable.length - 1) : NaN);
    if (Number.isFinite(position)) v.video.currentTime = position;
    // A seek never resumes a user-paused video.
  }

  function stopViewer(key) {
    const v = viewers.get(key);
    if (!v) return;
    v.closed = true; viewers.delete(key);
    v.hls?.destroy(); v.hls = null;
    v.internalPause = true; v.video.pause(); v.internalPause = false;
    v.video.removeAttribute('src'); v.video.load();
  }

  window.WbbMedia = { newPublisher, publisherVideo: key => publishers.get(key)?.preview,
    captureOffer, acceptAnswer, stopPublisher, resumeAudio, newViewer,
    viewerVideo: key => viewers.get(key)?.video, playViewer, goLive, stopViewer };
})();
