import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const routes: [string, string, number][] = [
    ["^/api/(products|sales|admin/(products|replicas))(?:/|$)", "CATALOG", 8201],
    ["^/api/admin/(sale-drafts|publications)(?:/|$)", "PUBLICATION", 8202],
    ["^/api/attempts(?:/|$)", "ADMISSION", 8204],
    ["^/api/orders(?:/|$)", "ORDERS", 8205],
    ["^/api/notifications(?:/|$)", "NOTIFICATION", 8207],
    ["^/api/analytics(?:/|$)", "ANALYTICS", 8208],
  ];
  return {
    server: {
      host: "127.0.0.1",
      proxy: Object.fromEntries(routes.map(([pattern, service, port]) =>
        [pattern, { target: env[`FLASHKART_${service}_ORIGIN`] || `http://127.0.0.1:${port}` }],
      )),
    },
  };
});
