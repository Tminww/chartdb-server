#!/bin/sh

API_BACKEND_URL="${API_BACKEND_URL:-http://backend:8080}"
API_BASE_URL="${API_BASE_URL:-/api}"

# Replace placeholders in nginx.conf
envsubst '${OPENAI_API_KEY} ${OPENAI_API_ENDPOINT} ${LLM_MODEL_NAME} ${API_BASE_URL} ${API_BACKEND_URL} ${HIDE_CHARTDB_CLOUD} ${DISABLE_ANALYTICS}' < /etc/nginx/conf.d/default.conf.template > /etc/nginx/conf.d/default.conf

# Start Nginx
nginx -g "daemon off;"
