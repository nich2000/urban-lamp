const cards = new Map();

function durationText(result) {
  if (!result || result.durationMs === null || result.durationMs === undefined) {
    return "—";
  }
  return `${result.durationMs} мс`;
}

function statusText(result, threshold) {
  if (!result) return { text: "ожидание", className: "status unknown" };
  if (!result.ok) return { text: "нет ответа", className: "status down" };
  if (result.durationMs >= threshold) return { text: "долгий пинг", className: "status slow" };
  return { text: "доступен", className: "status ok" };
}

function checkedAtText(result) {
  if (!result?.checkedAt) return "—";
  return new Intl.DateTimeFormat("ru-RU", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(result.checkedAt));
}

function serviceWord(count) {
  const mod100 = count % 100;
  if (mod100 >= 11 && mod100 <= 14) return "сервисов";
  switch (count % 10) {
    case 1:
      return "сервис";
    case 2:
    case 3:
    case 4:
      return "сервиса";
    default:
      return "сервисов";
  }
}

function showError(message) {
  const banner = document.querySelector("#app-error");
  if (!banner) return;
  banner.textContent = message;
  banner.hidden = !message;
}

function methodOptions(method) {
  return `
    <option value="http" ${method === "http" ? "selected" : ""}>http</option>
    <option value="icmp" ${method === "icmp" ? "selected" : ""}>icmp</option>`;
}

function ensureCard(snapshot) {
  const grid = document.querySelector("#services");
  let card = cards.get(snapshot.service.id);
  if (card) return card;

  card = document.querySelector(`[data-service-id="${snapshot.service.id}"]`);
  if (!card) {
    card = document.createElement("article");
    card.className = "service-card";
    card.dataset.serviceId = snapshot.service.id;
    card.innerHTML = `
      <div class="card-head">
        <div>
          <h2></h2>
          <p></p>
        </div>
        <div class="card-actions">
          <button class="ghost edit-toggle" type="button" aria-expanded="false">Изменить</button>
          <form method="post">
            <button class="ghost danger" type="submit">Удалить</button>
          </form>
        </div>
      </div>
      <form method="post" class="edit-form" hidden>
        <label>
          Цель
          <input name="target" required>
        </label>
        <label>
          Метод
          <select name="method" required></select>
        </label>
        <button type="submit">Сохранить</button>
      </form>
      <div class="metric-row">
        <span class="status unknown">ожидание</span>
        <div class="timing">
          <strong class="duration">—</strong>
          <time>—</time>
        </div>
      </div>
      <canvas height="130" aria-label="График продолжительности пинга"></canvas>
      <p class="last-error"></p>`;
    grid.appendChild(card);
  }
  cards.set(snapshot.service.id, card);
  return card;
}

function renderSnapshot(payload) {
  const grid = document.querySelector("#services");
  const threshold = payload.longPingThreshold || Number(grid.dataset.threshold || 800);
  const maxServices = Number(grid.dataset.maxServices || 20);
  const seen = new Set();

  document.querySelector("#service-count").textContent = payload.services.length;
  document.querySelector("#service-word").textContent = serviceWord(payload.services.length);
  const addButton = document.querySelector(".service-form button[type='submit']");
  if (addButton) addButton.disabled = payload.services.length >= maxServices;
  grid.querySelector(".empty")?.remove();

  for (const snapshot of payload.services) {
    seen.add(String(snapshot.service.id));
    const card = ensureCard(snapshot);
    const latest = snapshot.latest;
    const status = statusText(latest, threshold);

    card.querySelector("h2").textContent = snapshot.service.target;
    card.querySelector(".card-head p").textContent = snapshot.service.method;
    const editForm = card.querySelector(".edit-form");
    const editing = !editForm.hidden || editForm.matches(":focus-within");
    card.querySelector(".card-actions form").action = `/services/${snapshot.service.id}/delete`;
    editForm.action = `/services/${snapshot.service.id}/update`;
    if (!editing) {
      editForm.querySelector("input[name='target']").value = snapshot.service.target;
      editForm.querySelector("select[name='method']").innerHTML = methodOptions(snapshot.service.method);
    }
    card.querySelector(".status").className = status.className;
    card.querySelector(".status").textContent = status.text;
    card.querySelector(".duration").textContent = durationText(latest);
    card.querySelector(".timing time").textContent = checkedAtText(snapshot.lastOk);
    card.querySelector(".last-error").textContent = latest?.error || "";
    drawChart(card.querySelector("canvas"), snapshot.history, threshold);
  }

  for (const card of [...document.querySelectorAll(".service-card")]) {
    if (!seen.has(card.dataset.serviceId)) {
      cards.delete(Number(card.dataset.serviceId));
      card.remove();
    }
  }

  if (payload.services.length === 0) {
    grid.innerHTML = '<div class="empty">Добавьте первый сервис для мониторинга.</div>';
  }
}

function drawChart(canvas, history, threshold) {
  const dpr = window.devicePixelRatio || 1;
  const rect = canvas.getBoundingClientRect();
  canvas.width = Math.max(1, Math.floor(rect.width * dpr));
  canvas.height = Math.max(1, Math.floor(rect.height * dpr));

  const ctx = canvas.getContext("2d");
  ctx.scale(dpr, dpr);
  const width = rect.width;
  const height = rect.height;
  const pad = 12;
  const plotW = width - pad * 2;
  const plotH = height - pad * 2;
  const values = history.map((point) => point.ok ? point.durationMs : null);
  const maxValue = Math.max(threshold, ...values.filter((v) => v !== null), 100);

  ctx.clearRect(0, 0, width, height);
  ctx.strokeStyle = "#d9e0e8";
  ctx.lineWidth = 1;
  for (let i = 0; i < 4; i++) {
    const y = pad + (plotH / 3) * i;
    ctx.beginPath();
    ctx.moveTo(pad, y);
    ctx.lineTo(width - pad, y);
    ctx.stroke();
  }

  const thresholdY = pad + plotH - (Math.min(threshold, maxValue) / maxValue) * plotH;
  ctx.strokeStyle = "#d28b26";
  ctx.setLineDash([5, 5]);
  ctx.beginPath();
  ctx.moveTo(pad, thresholdY);
  ctx.lineTo(width - pad, thresholdY);
  ctx.stroke();
  ctx.setLineDash([]);

  if (history.length < 2) return;

  ctx.strokeStyle = "#0b65c2";
  ctx.lineWidth = 2;
  ctx.beginPath();
  let started = false;
  history.forEach((point, index) => {
    if (!point.ok || point.durationMs === null || point.durationMs === undefined) {
      started = false;
      return;
    }
    const x = pad + (plotW * index) / Math.max(history.length - 1, 1);
    const y = pad + plotH - (point.durationMs / maxValue) * plotH;
    if (!started) {
      ctx.moveTo(x, y);
      started = true;
    } else {
      ctx.lineTo(x, y);
    }
  });
  ctx.stroke();

  history.forEach((point, index) => {
    if (point.ok) return;
    const x = pad + (plotW * index) / Math.max(history.length - 1, 1);
    ctx.fillStyle = "#ba1a1a";
    ctx.beginPath();
    ctx.arc(x, height - pad, 3, 0, Math.PI * 2);
    ctx.fill();
  });
}

document.querySelectorAll(".service-card").forEach((card) => {
  cards.set(Number(card.dataset.serviceId), card);
});

document.addEventListener("click", (event) => {
  const button = event.target.closest(".edit-toggle");
  if (!button) return;

  const card = button.closest(".service-card");
  const form = card.querySelector(".edit-form");
  const isOpen = !form.hidden;
  form.hidden = isOpen;
  button.setAttribute("aria-expanded", String(!isOpen));
});

const events = new EventSource("/events");
events.addEventListener("snapshot", (event) => {
  try {
    renderSnapshot(JSON.parse(event.data));
  } catch (error) {
    showError("Не удалось обновить данные на странице.");
  }
});
events.addEventListener("app-error", (event) => {
  try {
    showError(JSON.parse(event.data));
  } catch (error) {
    showError("Данные временно недоступны.");
  }
});
events.addEventListener("error", () => {
  showError("Соединение с сервером временно недоступно.");
});
