const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");

const blockingStatuses = new Set(["draining", "completed", "failed"]);
const isBlocking = (chamber) => chamber.emergency && blockingStatuses.has(chamber.emergency.status);

async function refresh() {
  const [plans, chambers, health] = await Promise.all([api("/api/plans"), api("/api/chambers"), api("/healthz")]);
  document.querySelector("#health").textContent = health.status === "ok" ? "Devices online" : "Attention required";
  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => `<tr><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td><td>${new Date(plan.created_at).toLocaleTimeString()}</td></tr>`));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="5" class="muted">No active plans</td></tr>';

  // Prefer a live (non-terminal) transit, then any chamber in an emergency latch.
  const current = chambers.find((item) => item.cycle && !["completed", "cancelled", "emergency"].includes(item.cycle.stage))
    || chambers.find(isBlocking)
    || chambers.find((item) => item.cycle);
  document.querySelector("#metric-chamber").textContent = current?.chamber_id || "Idle";
  document.querySelector("#metric-stage").textContent = current?.cycle?.stage || "-";
  document.querySelector("#metric-cycle").textContent = shortID(current?.cycle?.id);
  document.querySelector("#metric-pending").textContent = current?.pending_commands?.length ?? 0;

  const blocked = chambers.find(isBlocking);
  const banner = document.querySelector("#emergency-banner");
  if (blocked) {
    const record = blocked.emergency;
    banner.hidden = false;
    banner.dataset.chamber = blocked.chamber_id;
    document.querySelector("#emergency-status").textContent = `${blocked.chamber_id}: emergency ${record.status}`;
    document.querySelector("#emergency-message").textContent = record.message ? ` — ${record.message}` : "";
    document.querySelector("#emergency-reset").disabled = record.status === "draining";
  } else {
    banner.hidden = true;
    banner.dataset.chamber = "";
  }

  // A chamber under an unreset emergency cannot accept a new convoy plan.
  document.querySelectorAll("#chamber option").forEach((option) => {
    const chamber = chambers.find((item) => item.chamber_id === option.value);
    option.disabled = Boolean(chamber && isBlocking(chamber));
  });
}

document.querySelector("#emergency-reset").addEventListener("click", async (event) => {
  const banner = document.querySelector("#emergency-banner");
  const chamberID = banner.dataset.chamber;
  if (!chamberID) return;
  await withBusy(event.target, async () => {
    try {
      await api(`/api/chambers/${chamberID}/emergency/reset`, { method: "POST", body: "{}" });
      setMessage(message, `Emergency reset for ${chamberID}. New transits are permitted again.`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = form.querySelector("button");
  await withBusy(button, async () => {
    try {
      const result = await api("/api/plans", { method: "POST", body: JSON.stringify({
        chamber_id: document.querySelector("#chamber").value,
        direction: document.querySelector("#direction").value,
        vessels: [{ name: document.querySelector("#vessel").value, call_sign: document.querySelector("#call-sign").value, length_m: Number(document.querySelector("#length").value) }],
      }) });
      setMessage(message, `Plan ${shortID(result.plan.id)} admitted to ${result.plan.chamber_id}.`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

refresh().catch((error) => setMessage(message, error.message));
setInterval(() => refresh().catch(() => {}), 5000);
