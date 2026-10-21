import type { Monitor } from "./types";

function drawFavicon(down: number) {
  const canvas = document.createElement("canvas");
  canvas.width = 64;
  canvas.height = 64;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  const link = document.querySelector<HTMLLinkElement>("link[rel*='icon']");

  const bg = down > 0 ? "#ef4444" : "#22c55e";
  ctx.clearRect(0, 0, 64, 64);
  ctx.fillStyle = bg;
  ctx.beginPath();
  ctx.arc(32, 28, 18, 0, Math.PI * 2);
  ctx.fill();

  ctx.strokeStyle = "#ffffff";
  ctx.lineWidth = 4;
  ctx.lineCap = "round";
  if (down > 0) {
    ctx.beginPath();
    ctx.moveTo(24, 20);
    ctx.lineTo(40, 36);
    ctx.moveTo(40, 20);
    ctx.lineTo(24, 36);
    ctx.stroke();
  } else {
    ctx.beginPath();
    ctx.moveTo(23, 28);
    ctx.lineTo(29, 34);
    ctx.lineTo(41, 22);
    ctx.stroke();
  }

  if (down > 0) {
    ctx.fillStyle = "#ef4444";
    ctx.beginPath();
    ctx.arc(50, 50, 14, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = "#ffffff";
    ctx.font = "bold 19px sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText(down > 99 ? "99+" : String(down), 50, 51);
  }

  if (link) link.href = canvas.toDataURL("image/svg+xml") === "" ? link.href : canvas.toDataURL("image/png");
}

export function updateFavicon(monitors: Monitor[] | null, productName: string) {
  const down = (monitors ?? []).filter(
    (m) => m.enabled && m.state?.status === "down",
  ).length;
  drawFavicon(down);
  document.title = down > 0 ? `(${down} down) · ${productName}` : productName;
}
