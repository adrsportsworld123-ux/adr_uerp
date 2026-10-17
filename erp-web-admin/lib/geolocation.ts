// The browser's location for an attendance punch. The server records the
// IP it sees itself; only the coordinates come from here.

export type Fix = { latitude: number; longitude: number; accuracy_m: number };
export type FixResult = { fix: Fix } | { fix: null; reason: string };

export function getFix(timeoutMs = 15000): Promise<FixResult> {
  if (typeof window === "undefined" || !("geolocation" in navigator)) {
    return Promise.resolve({ fix: null, reason: "This browser can't share a location." });
  }
  if (!window.isSecureContext) {
    return Promise.resolve({ fix: null, reason: "Location needs a secure (https) connection to this site." });
  }
  return new Promise((resolve) => {
    navigator.geolocation.getCurrentPosition(
      (p) =>
        resolve({
          fix: { latitude: p.coords.latitude, longitude: p.coords.longitude, accuracy_m: Math.round(p.coords.accuracy) },
        }),
      (e) =>
        resolve({
          fix: null,
          reason:
            e.code === e.PERMISSION_DENIED
              ? "Location access was blocked. Allow it for this site in the browser's address bar, then try again."
              : e.code === e.TIMEOUT
                ? "Getting your location took too long."
                : "Your location isn't available right now.",
        }),
      { enableHighAccuracy: true, timeout: timeoutMs, maximumAge: 30000 }
    );
  });
}

export function mapsLink(lat: number, lng: number): string {
  return `https://www.google.com/maps?q=${lat},${lng}`;
}
