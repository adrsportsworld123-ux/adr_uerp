"use client";

// Check-in / check-out for whoever is signed in (erp-core-go
// internal/hr/attendance.go). The browser supplies the location; the
// server records the IP and device itself and places the punch against
// the branch geofence under the merchant's rules.
import { useCallback, useEffect, useState } from "react";
import { Clock, Loader2, MapPin } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { AttendanceRecord, MyAttendance } from "@/lib/types";
import { getFix, Fix } from "@/lib/geolocation";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";

function time(iso?: string) {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString("en-IN", { hour: "2-digit", minute: "2-digit" });
}

const GEOFENCE_LABEL: Record<string, string> = {
  inside: "at the branch",
  outside: "outside the branch area — flagged for your manager",
  unknown: "without a location",
  not_configured: "",
};

export function AttendanceButton() {
  const [me, setMe] = useState<MyAttendance | null>(null);
  const [open, setOpen] = useState(false);
  const [locating, setLocating] = useState(false);
  const [fix, setFix] = useState<Fix | null>(null);
  const [fixError, setFixError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<MyAttendance>("/api/v1/hr/attendance/me")
      .then(setMe)
      .catch(() => setMe(null)); // an older backend: no button
  }, []);
  useEffect(load, [load]);

  // Any number of check-ins a day (migrations/053): checked in while a
  // session is open; otherwise the button always offers a new check-in.
  const checkedIn = me?.open != null;
  const action = checkedIn ? "out" : "in";
  const workedToday = me?.today?.hours_worked && Number(me.today.hours_worked) > 0 ? me.today.hours_worked : null;
  const sessionsToday = me?.today?.sessions?.length ?? 0;

  async function locate() {
    setLocating(true);
    setFixError(null);
    const r = await getFix();
    setFix(r.fix);
    if (!r.fix) setFixError(r.reason);
    setLocating(false);
  }

  function start() {
    setError(null);
    setFix(null);
    setOpen(true);
    void locate();
  }

  async function punch() {
    setBusy(true);
    setError(null);
    try {
      const rec = await api.post<AttendanceRecord>(`/api/v1/hr/attendance/clock-${action}`, fix ?? undefined);
      const ev = action === "in" ? rec.clock_in_evidence : rec.clock_out_evidence;
      const where = ev.geofence ? GEOFENCE_LABEL[ev.geofence] : "";
      toast.success(`Checked ${action} at ${time(action === "in" ? rec.clock_in : rec.clock_out)}${where ? ` ${where}` : ""}`);
      setOpen(false);
      load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : `Could not check ${action}`);
    } finally {
      setBusy(false);
    }
  }

  if (!me) return null;
  const needsLocation = me.settings.require_location || me.settings.geofence_mode === "block";

  return (
    <>
      <Button
        variant={checkedIn ? "outline" : "default"}
        size="sm"
        onClick={start}
        title={workedToday ? `${workedToday} h worked today over ${sessionsToday} session(s)` : undefined}
      >
        <Clock aria-hidden />
        {checkedIn ? `In since ${time(me.open_since ?? me.open?.clock_in)} · Check out` : sessionsToday > 0 ? "Check in again" : "Check in"}
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={action === "in" ? "Check in" : "Check out"}
        description={new Date().toLocaleString("en-IN", { weekday: "long", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}
        footer={
          <>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={punch} disabled={busy || locating || (needsLocation && !fix)}>
              {busy && <Loader2 className="animate-spin" aria-hidden />}
              {action === "in" ? "Check in now" : "Check out now"}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3 text-sm">
          <div className="flex items-start gap-2">
            <MapPin className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
            {locating ? (
              <span className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" aria-hidden /> Getting your location…
              </span>
            ) : fix ? (
              <span>
                Location found (accurate to about {fix.accuracy_m} m)
                {me.geofence && <span className="block text-muted-foreground">Checked against {me.geofence.branch_name}&apos;s {me.geofence.radius_m} m area.</span>}
              </span>
            ) : (
              <span className="flex flex-col gap-1">
                <span className={needsLocation ? "text-destructive" : "text-muted-foreground"}>{fixError}</span>
                {needsLocation ? (
                  <span className="text-muted-foreground">Your company requires your location to check {action}.</span>
                ) : (
                  <span className="text-muted-foreground">You can still check {action}; it will be recorded without a location.</span>
                )}
                <Button variant="outline" size="sm" className="self-start" onClick={locate}>
                  Try again
                </Button>
              </span>
            )}
          </div>
          <p className="text-xs text-muted-foreground">Your network address and device are recorded with the time.</p>
          {error && <p className="text-destructive">{error}</p>}
        </div>
      </Modal>
    </>
  );
}
