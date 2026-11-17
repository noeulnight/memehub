import { fileURLToPath } from 'node:url';
import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import node from '@astrojs/node';
import tailwindcss from '@tailwindcss/vite';
import { loadEnv } from 'vite-plus';

const env = loadEnv(
  process.env.NODE_ENV || 'development',
  fileURLToPath(new URL('../../', import.meta.url)),
  [
    'PUBLIC_API_BASE_URL',
    'DEV_API_PROXY_TARGET',
    'DEV_API_READ_ONLY',
    'APP_BASE_URL',
    'API_INTERNAL_BASE_URL',
  ],
);

export default defineConfig({
  integrations: [react()],
  output: 'server',
  adapter: node({ mode: 'standalone' }),
  vite: {
    plugins: [
      tailwindcss(),
      ...(env.DEV_API_READ_ONLY === 'true'
        ? [
            {
              name: 'read-only-api-preview',
              configureServer(server) {
                server.middlewares.use((req, res, next) => {
                  const path = new URL(req.url, 'http://localhost').pathname;
                  if (!path.startsWith('/api/')) return next();
                  const allowed =
                    req.method === 'GET' &&
                    /^\/api\/(?:memes(?:\/[0-9a-f-]+(?:\/media)?)?|packs|tags|search\/suggestions)$/.test(
                      path,
                    );
                  if (allowed) return next();
                  res.statusCode = path === '/api/auth/me' ? 401 : 403;
                  res.setHeader('Content-Type', 'application/json');
                  res.end(JSON.stringify({ error: 'read_only_preview' }));
                });
              },
            },
          ]
        : []),
    ],
    define: {
      'import.meta.env.API_INTERNAL_BASE_URL': JSON.stringify(
        env.API_INTERNAL_BASE_URL || `${env.DEV_API_PROXY_TARGET || 'http://127.0.0.1:8080'}/api`,
      ),
      'import.meta.env.PUBLIC_API_BASE_URL': JSON.stringify(env.PUBLIC_API_BASE_URL || '/api'),
      'import.meta.env.PUBLIC_APP_BASE_URL': JSON.stringify(env.APP_BASE_URL || ''),
    },
    server: {
      proxy: {
        '/api': {
          target: env.DEV_API_PROXY_TARGET || 'http://127.0.0.1:8080',
          changeOrigin: true,
          configure(proxy) {
            if (env.DEV_API_READ_ONLY !== 'true') return;
            proxy.on('proxyReq', (request) => {
              for (const header of ['cookie', 'authorization', 'x-csrf-token'])
                request.removeHeader(header);
            });
          },
        },
      },
    },
  },
});
