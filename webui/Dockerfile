FROM node:20-alpine AS build
WORKDIR /ui
# Copy lockfile for reproducible builds
COPY package.json package-lock.json ./
RUN npm ci
# Copy source after installing dependencies to leverage Docker layer cache
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /ui/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 3000
CMD ["nginx", "-g", "daemon off;"]


