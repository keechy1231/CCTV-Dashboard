FROM node:20-bookworm-slim AS frontend
WORKDIR /app
COPY frontend/package.json ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=frontend /app/dist /usr/share/nginx/html
COPY nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY nginx/40-configure.sh /docker-entrypoint.d/40-configure.sh
EXPOSE 80
