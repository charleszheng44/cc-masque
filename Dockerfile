FROM node:lts-alpine

RUN apk add --no-cache git bash github-cli \
 && npm install -g @anthropic-ai/claude-code \
 && rm -rf /root/.npm

ENV SHELL=/bin/bash

# Pre-seed config so the first run skips onboarding (theme, trust, etc.)
RUN printf '%s' '{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}' \
    > /root/.claude.json

COPY scripts/cc-crew-run /usr/local/bin/cc-crew-run
RUN chmod +x /usr/local/bin/cc-crew-run

WORKDIR /workspace

# If CC_ROLE is set, run the cc-crew entrypoint (dispatched by the
# orchestrator). Otherwise, keep the container alive with `tail -f`
# for manual `docker exec` workflows (matches pre-cc-crew behaviour).
ENTRYPOINT ["/bin/sh", "-c", "if [ -n \"${CC_ROLE:-}\" ]; then exec /usr/local/bin/cc-crew-run; else exec \"$@\"; fi", "--"]
CMD ["tail", "-f", "/dev/null"]
