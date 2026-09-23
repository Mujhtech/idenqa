"""Private bounded inference subprocess. Never logs inputs or native errors."""
import base64
import hashlib
import importlib.metadata
import json
import math
import os
import platform
import sys


def run():
    # Full opt-out must precede native environment initialization.
    if os.environ.get("ORT_DISABLE_TELEMETRY") != "1":
        raise ValueError("telemetry must be disabled")
    import cv2
    cv2.setNumThreads(1)
    cv2.ocl.setUseOpenCL(False)
    import numpy as np
    import onnxruntime as ort
    ort.disable_telemetry_events()

    if ort.__version__ != "1.29.0":
        raise ValueError("runtime version")
    # Native wheels and installed dependency files contribute to the runtime pin.
    fingerprint = hashlib.sha256()
    fingerprint.update(sys.version.encode())
    fingerprint.update(platform.machine().encode())
    fingerprint.update(platform.system().encode())
    for name in ("onnxruntime", "numpy", "flatbuffers", "packaging", "protobuf", "opencv-python-headless"):
        distribution = importlib.metadata.distribution(name)
        fingerprint.update((name + "=" + distribution.version).encode())
        for file in sorted(distribution.files or [], key=str):
            if str(file).endswith((".pyc", ".pyo")):
                continue
            # Hash installed wheel contents as well as their RECORD identities.
            if file.hash:
                fingerprint.update((str(file) + ":" + file.hash.value).encode())
            if file.hash:
                with open(distribution.locate_file(file), "rb") as native:
                    while chunk := native.read(1024 * 1024):
                        fingerprint.update(chunk)
    fingerprint.update(SCRIPT_DIGEST.encode())
    runtime_digest = "sha256:" + fingerprint.hexdigest()
    raw = sys.stdin.buffer.read(96 * 1024 * 1024 + 1)
    if len(raw) > 96 * 1024 * 1024:
        raise ValueError("input bound")
    request = json.loads(raw)
    if request["operation"] == "runtime":
        return {"runtime_digest": runtime_digest}
    if request["runtime_digest"] != runtime_digest:
        raise ValueError("runtime mismatch")
    options = ort.SessionOptions()
    options.intra_op_num_threads = 1
    options.inter_op_num_threads = 1
    options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
    options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_DISABLE_ALL
    options.enable_cpu_mem_arena = False
    options.enable_mem_pattern = False
    options.log_severity_level = 4
    if request.get("mode") == "face_analysis":
        detector = detector_session(request, options)
        if request["operation"] == "validate":
            return {"runtime_digest": runtime_digest}
        if request["operation"] != "analysis":
            raise ValueError("analysis operation")
        return {"runtime_digest": runtime_digest, "codes": analyze_image(request, detector)}
    model = base64.b64decode(request["model"], validate=True)
    if not model or len(model) > 64 * 1024 * 1024:
        raise ValueError("model bound")
    if "sha256:" + hashlib.sha256(model).hexdigest() != request["model_digest"]:
        raise ValueError("model mismatch")
    # Byte loading rejects filesystem-dependent external tensor data. No custom
    # operators, model downloads or execution-provider fallback are configured.
    session = ort.InferenceSession(model, sess_options=options, providers=["CPUExecutionProvider"])
    session.disable_fallback()
    inputs, outputs = session.get_inputs(), session.get_outputs()
    shape = request["shape"]
    matching = request.get("mode") == "face_match"
    output_width = 512 if matching else 2
    if (len(inputs) != 1 or len(outputs) != 1
            or inputs[0].name != "input" or inputs[0].type != "tensor(float)"
            or inputs[0].shape[1:] != shape[1:] or inputs[0].shape[0] not in (1, "batch_size")
            or outputs[0].name != "output" or outputs[0].type != "tensor(float)"
            or outputs[0].shape[1:] != [output_width] or outputs[0].shape[0] not in (1, "batch_size")):
        raise ValueError("tensor schema")
    detector = detector_session(request, options) if request.get("detector") else None
    if request["operation"] == "validate":
        return {"runtime_digest": runtime_digest}
    if matching:
        if detector is None or request["operation"] != "pair":
            raise ValueError("matching detector and pair required")
        return match_pair(request, detector, session, runtime_digest)
    if request["operation"] == "image":
        if detector is None:
            raise ValueError("detector required")
        tensor, reason = prepare_image(request, detector)
        if reason:
            return {"runtime_digest": runtime_digest, "reason": reason}
    else:
        values = request["values"]
        if not all(isinstance(x, (float, int)) and math.isfinite(x) for x in values):
            raise ValueError("nonfinite input")
        tensor = np.asarray(values, dtype=np.float32).reshape(shape)
    logits = session.run(["output"], {"input": tensor})[0]
    if logits.shape != (1, 2) or not np.isfinite(logits).all():
        raise ValueError("output schema")
    weights = np.exp(logits[0].astype(np.float64) - np.max(logits[0]))
    score = weights / weights.sum()
    if score.shape != (2,) or not np.isfinite(score).all():
        raise ValueError("output schema")
    value = float(score[0])
    if not 0 <= value <= 1:
        raise ValueError("score range")
    return {"runtime_digest": runtime_digest, "score": value}


try:
    result = run()
    sys.stdout.write(json.dumps(result, allow_nan=False, separators=(",", ":")))
except BaseException:
    # Native output is discarded by the parent, including initialization errors.
    sys.exit(1)
