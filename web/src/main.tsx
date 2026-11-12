import { createRoot } from "react-dom/client";

const root = document.getElementById("root");
if (!root) {
  throw new Error("Missing Flashkart root element");
}

createRoot(root).render(
  <main>
    <h1>Flashkart</h1>
    <p>Storefront setup is in progress.</p>
  </main>,
);
