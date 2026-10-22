// Pure directional walk selection. Keeping this out of virtual.js makes the
// angle thresholds testable without a browser or a VRM model.
//
// Screen coordinates: dx > 0 is right, dy > 0 is down/towards the viewer.
// The imported clips only face front, left/right profile, and the two
// three-quarter angles. There is no back-facing clip, so moving away from the
// viewer reuses the front clip.
export function selectWalkClipKey(dx, dy) {
    const ax = Math.abs(Number(dx) || 0);
    const ay = Math.abs(Number(dy) || 0);
    if (ax < 1 && ay < 1) return 'walk-front';

    // Horizontal movement reads as a profile. The 1.5 ratio keeps a slight
    // vertical component from turning a side-step into a diagonal.
    if (ax >= ay * 1.5) {
        return dx < 0 ? 'walk-left-profile' : 'walk-right-profile';
    }

    // Mostly vertical: front is the only honest option without a back clip.
    if (ay >= ax * 1.5) {
        return 'walk-front';
    }

    return dx < 0 ? 'walk-three-quarter-left' : 'walk-three-quarter-right';
}
