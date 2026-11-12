import unittest

from hold_continuity import verify_hold_continuity


def samples(frozen_from=None, frozen_until=None, audio_frozen=False):
    points = []
    frames = 0
    media_time = 0.0
    for t in range(0, 10001, 250):
        frozen = frozen_from is not None and frozen_from <= t < frozen_until
        if not frozen:
            frames += 8
            media_time += .25
        a, b = (-105, -105) if frozen and audio_frozen else (-48, -49)
        points.append([t, frames, media_time, a, b, -100])
    return points


class HoldContinuityTest(unittest.TestCase):
    def test_steady_video_and_two_tones(self):
        result = verify_hold_continuity(samples(), 1000, 8000)
        self.assertLessEqual(result['maxVideoStallMs'], 2500)
        self.assertGreater(result['audioCount'], 2)

    def test_aggregate_frames_and_tones_hide_seven_second_stall(self):
        # The previous whole-hold frame total and 12s stall bound passed;
        # audio remained good, so the old 5s FFT check also passed.
        frozen = samples(1000, 8000, audio_frozen=False)
        self.assertGreater(frozen[-1][1] - frozen[0][1], 20)
        with self.assertRaisesRegex(ValueError, 'video stalled'):
            verify_hold_continuity(frozen, 1000, 8000)

    def test_audio_loss_with_video_progress_is_rejected(self):
        frozen = samples(1000, 8000, audio_frozen=True)
        for point in frozen:
            point[1] = point[0] // 30
            point[2] = point[0] / 1000
        with self.assertRaisesRegex(ValueError, 'audio stalled'):
            verify_hold_continuity(frozen, 1000, 8000)


if __name__ == '__main__':
    unittest.main()
