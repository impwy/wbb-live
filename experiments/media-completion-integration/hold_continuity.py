"""Align B's decoded media with the exact A barrier/Stop interval."""
import math

MAX_SAMPLE_GAP_MS = 1500
MAX_MEDIA_STALL_MS = 2500


def verify_hold_continuity(samples, start_ms, end_ms):
    if not isinstance(start_ms, (int, float)) or not isinstance(end_ms, (int, float)) or end_ms - start_ms < 6000:
        raise ValueError('A hold interval is missing or too short')
    points = sorted((p for p in samples if isinstance(p, list) and len(p) == 6
                     and isinstance(p[0], (int, float)) and start_ms - 500 <= p[0] <= end_ms + 500),
                    key=lambda p: p[0])
    if len(points) < 5 or points[0][0] > start_ms + 750 or points[-1][0] < end_ms - 750:
        raise ValueError('B samples do not cover A hold')
    last_progress = last_audio = start_ms
    previous = points[0]
    max_sample_gap = max_progress_gap = max_audio_gap = 0
    progress_count = audio_count = 0
    for point in points:
        t, frames, media_time, a, b, noise = point
        if not all(isinstance(x, (int, float)) and math.isfinite(x) for x in point):
            raise ValueError('invalid B media sample')
        gap = t - previous[0]
        max_sample_gap = max(max_sample_gap, gap)
        if gap > MAX_SAMPLE_GAP_MS:
            raise ValueError(f'B observation gap {gap:.0f}ms')
        if frames > previous[1] and media_time > previous[2] + .01:
            last_progress = t
            progress_count += 1
        if a > -65 and b > -65 and a > noise + 6 and b > noise + 6:
            last_audio = t
            audio_count += 1
        max_progress_gap = max(max_progress_gap, t - last_progress)
        max_audio_gap = max(max_audio_gap, t - last_audio)
        if t - last_progress > MAX_MEDIA_STALL_MS:
            raise ValueError(f'B video stalled {t - last_progress:.0f}ms during A hold')
        if t - last_audio > MAX_MEDIA_STALL_MS:
            raise ValueError(f'B two-tone audio stalled {t - last_audio:.0f}ms during A hold')
        previous = point
    if end_ms - last_progress > MAX_MEDIA_STALL_MS or end_ms - last_audio > MAX_MEDIA_STALL_MS:
        raise ValueError('B media stopped before A hold ended')
    if progress_count < 2 or audio_count < 2:
        raise ValueError('B media progress was not repeatedly observed')
    return {'sampleCount': len(points), 'progressCount': progress_count,
            'audioCount': audio_count, 'maxSampleGapMs': round(max_sample_gap),
            'maxVideoStallMs': round(max_progress_gap), 'maxAudioStallMs': round(max_audio_gap)}
