# Multi-stage build: compile the static site, then serve it via a minimal
# nginx image — no Node runtime needed in the final image, consistent with
# the distroless-final-image philosophy used by the Go services'
# Dockerfiles (Technical Roadmap.md §9).
FROM node:22-alpine AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
