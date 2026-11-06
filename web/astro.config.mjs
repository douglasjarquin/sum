import { defineConfig } from "astro/config";
import sitemap from "@astrojs/sitemap";

export default defineConfig({
  base: "/sum",
  output: "static",
  site: "https://douglasjarquin.github.io",
  integrations: [sitemap()],
  trailingSlash: "always"
});
