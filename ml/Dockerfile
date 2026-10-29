FROM python:3.12.14-slim-bookworm
WORKDIR /app
COPY ml/requirements*.txt ml/
RUN python -m pip install --no-cache-dir --no-deps -r ml/requirements.linux.lock.txt --extra-index-url https://download.pytorch.org/whl/cpu && python -m pip check
COPY ml/src/ ml/src/
COPY models/ models/
ENV PYTHONPATH=/app/ml/src HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HF_HUB_DISABLE_TELEMETRY=1 HF_HUB_DISABLE_IMPLICIT_TOKEN=1 ML_HOST=0.0.0.0 ML_PORT=8090
# Verify and warm exactly the approved artifacts at build time as well as boot.
RUN python -c "from pause_ml.runtime import Runtime; Runtime()"
USER 65532:65532
EXPOSE 8090
CMD ["python", "-B", "-m", "pause_ml.service"]
