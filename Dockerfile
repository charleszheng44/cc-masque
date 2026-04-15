FROM node:lts-alpine

RUN apk add --no-cache git bash github-cli \
 && npm install -g @anthropic-ai/claude-code \
 && rm -rf /root/.npm

ENV SHELL=/bin/bash

# Pre-seed config so the first run skips onboarding (theme, trust, etc.)
RUN printf '%s' '{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}' \
    > /root/.claude.json

WORKDIR /workspace

# Keep the container alive so `docker exec` can send commands into it.
CMD ["tail", "-f", "/dev/null"]
