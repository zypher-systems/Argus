import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
// Bundled so air-gapped installs never fetch a font. Plex Sans carries the
// interface and the large numbers, Plex Mono anything in a table.
import "@fontsource-variable/ibm-plex-sans";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import App from "./App";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
