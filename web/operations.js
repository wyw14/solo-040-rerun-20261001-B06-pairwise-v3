const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");
const emergencyMessage = document.querySelector("#emergency-message");

function blockingStatus(status) {
  return status === "draining" || status === "completed" || status === "failed";
}

async function resetChamber(chamber, button) {
  await withBusy(button, async () => {
    try {
      await api(`/api/chambers/${chamber}/emergency/reset`, { method: "POST", body: "{}" });
      setMessage(emergencyMessage, `${chamber} emergency reset.`);
      await refresh();
    } catch (error) { setMessage(emergencyMessage, error.message); }
  });
}

async function refresh() {
  const [plans, chambers, health] = await Promise.all([api("/api/plans"), api("/api/chambers"), api("/healthz")]);
  document.querySelector("#health").textContent = health.status === "ok" ? "Devices online" : "Attention required";
  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => `<tr><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td><td>${new Date(plan.created_at).toLocaleTimeString()}</td></tr>`));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="5" class="muted">No active plans</td></tr>';
  const current = chambers.find((item) => item.cycle);
  document.querySelector("#metric-chamber").textContent = current?.chamber_id || "Idle";
  document.querySelector("#metric-stage").textContent = current?.cycle?.stage || "-";
  document.querySelector("#metric-cycle").textContent = shortID(current?.cycle?.id);

  const emergencyRows = chambers.map((item) => {
    const status = item.emergency?.status || "idle";
    const canReset = status === "completed" || status === "failed";
    const detail = status === "draining"
      ? `draining &middot; ${item.pending_equipment ?? 0} device(s) unconfirmed`
      : status;
    const action = canReset
      ? `<button data-reset="${item.chamber_id}">Reset lockdown</button>`
      : status === "draining" ? '<span class="muted">in progress</span>' : "";
    return `<tr><td>${item.chamber_id}</td><td>${detail}</td><td>${item.pending_equipment ?? 0} pending / ${item.pending_commands ?? 0} cmd</td><td>${action}</td></tr>`;
  });
  document.querySelector("#emergency").innerHTML = emergencyRows.join("");
  document.querySelectorAll("[data-reset]").forEach((button) =>
    button.addEventListener("click", () => resetChamber(button.dataset.reset, button)));

  const selected = document.querySelector("#chamber").value;
  const selectedChamber = chambers.find((item) => item.chamber_id === selected);
  const locked = selectedChamber && blockingStatus(selectedChamber.emergency?.status);
  const submit = form.querySelector("button");
  submit.disabled = Boolean(locked);
  if (locked) {
    setMessage(message, `${selected} is in emergency lockdown (${selectedChamber.emergency.status}). Reset is required before a new transit.`);
  } else if (message.dataset.lockdown === "true") {
    setMessage(message, "");
  }
  message.dataset.lockdown = locked ? "true" : "false";
}

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

document.querySelector("#chamber").addEventListener("change", () => refresh().catch((error) => setMessage(message, error.message)));

refresh().catch((error) => setMessage(message, error.message));
setInterval(() => refresh().catch(() => {}), 5000);
