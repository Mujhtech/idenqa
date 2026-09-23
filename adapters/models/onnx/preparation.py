"""Owned evaluation preprocessing. No image or face coordinates leave this process."""


def detector_session(request, options):
    import base64
    import hashlib
    import onnxruntime as ort
    raw = base64.b64decode(request["detector"], validate=True)
    if not raw or len(raw) > 4 * 1024 * 1024:
        raise ValueError("detector bound")
    if "sha256:" + hashlib.sha256(raw).hexdigest() != request["detector_digest"]:
        raise ValueError("detector mismatch")
    session = ort.InferenceSession(raw, sess_options=options, providers=["CPUExecutionProvider"])
    session.disable_fallback()
    inputs = session.get_inputs()
    if len(inputs) != 1 or inputs[0].name != "input" or inputs[0].type != "tensor(float)" or inputs[0].shape != [1, 3, 640, 640]:
        raise ValueError("detector input")
    expected = {f"{kind}_{stride}": [1, (640 // stride) ** 2, size]
                for kind, size in (("cls", 1), ("obj", 1), ("bbox", 4), ("kps", 10))
                for stride in (8, 16, 32)}
    outputs = session.get_outputs()
    if len(outputs) != len(expected) or any(o.name not in expected or o.type != "tensor(float)" or o.shape != expected[o.name] for o in outputs):
        raise ValueError("detector output")
    return session


def locate_face(rgb, session):
    import cv2
    import numpy as np
    height, width = rgb.shape[:2]
    ratio = 640 / max(height, width)
    scaled_w, scaled_h = max(1, int(width * ratio)), max(1, int(height * ratio))
    # Preserve aspect ratio and explicitly pin interpolation/padding semantics.
    resized = cv2.resize(rgb[:, :, ::-1], (scaled_w, scaled_h), interpolation=cv2.INTER_LINEAR)
    padded = np.zeros((640, 640, 3), dtype=np.uint8)
    padded[:scaled_h, :scaled_w] = resized
    outputs = dict(zip((o.name for o in session.get_outputs()), session.run(None, {"input": padded.transpose(2, 0, 1)[None].astype(np.float32)})))
    boxes, scores, landmarks = [], [], []
    for stride in (8, 16, 32):
        side = 640 // stride
        for kind, size in (("cls", 1), ("obj", 1), ("bbox", 4), ("kps", 10)):
            value = outputs[f"{kind}_{stride}"]
            if value.shape != (1, side * side, size) or not np.isfinite(value).all():
                raise ValueError("invalid detection")
        score = np.sqrt(np.clip(outputs[f"cls_{stride}"][0, :, 0], 0, 1) * np.clip(outputs[f"obj_{stride}"][0, :, 0], 0, 1))
        for index in np.flatnonzero(score >= 0.8):
            dx, dy, log_w, log_h = outputs[f"bbox_{stride}"][0, index]
            if abs(float(log_w)) > 16 or abs(float(log_h)) > 16:
                raise ValueError("unbounded detection")
            w, h = np.exp(float(log_w)) * stride, np.exp(float(log_h)) * stride
            x = (index % side + float(dx)) * stride - w / 2
            y = (index // side + float(dy)) * stride - h / 2
            boxes.append([int(x), int(y), int(w), int(h)])
            scores.append(float(score[index]))
            points = outputs[f"kps_{stride}"][0, index]
            landmarks.append([[((index % side) + float(points[2 * point])) * stride,
                               ((index // side) + float(points[2 * point + 1])) * stride]
                              for point in range(5)])
    if not boxes:
        return None, "face_not_found"
    # OpenCV's YuNet wrapper uses integer-box NMS; stable input order breaks ties.
    keep = cv2.dnn.NMSBoxes(boxes, scores, 0.8, 0.3, top_k=5000)
    if len(keep) != 1:
        return None, "multiple_faces" if len(keep) > 1 else "face_not_found"
    selected = int(np.asarray(keep).reshape(-1)[0])
    x, y, w, h = boxes[selected]
    # Separate effective scales account for integer resize dimensions.
    x, y, w, h = int(x * width / scaled_w), int(y * height / scaled_h), int(w * width / scaled_w), int(h * height / scaled_h)
    if min(w, h) < 64:
        return None, "face_too_small"
    if min(x, y, width - x - w, height - y - h) < 5:
        return None, "face_at_edge"
    points = np.asarray(landmarks[selected], dtype=np.float64)
    points[:, 0] *= width / scaled_w
    points[:, 1] *= height / scaled_h
    return (x, y, w, h, points), ""


def contextual_tensor(rgb, face):
    import cv2
    import numpy as np
    x, y, w, h = face[:4]
    height, width = rgb.shape[:2]
    side = int(max(w, h) * 1.5)
    left, top = int(x + w / 2 - max(w, h) * 1.5 / 2), int(y + h / 2 - max(w, h) * 1.5 / 2)
    x1, y1, x2, y2 = max(0, left), max(0, top), min(width, left + side), min(height, top + side)
    if side <= 0 or side > 6144 or x2 <= x1 or y2 <= y1:
        raise ValueError("invalid crop")
    crop = cv2.copyMakeBorder(rgb[y1:y2, x1:x2], max(0, -top), max(0, top + side - height), max(0, -left), max(0, left + side - width), cv2.BORDER_REFLECT_101)
    if crop.shape != (side, side, 3):
        raise ValueError("invalid crop shape")
    resized = cv2.resize(crop, (128, 128), interpolation=cv2.INTER_LANCZOS4 if side < 128 else cv2.INTER_AREA)
    return resized.transpose(2, 0, 1)[None].astype(np.float32) / 255.0


def prepare_image(request, detector):
    import base64
    import cv2
    import numpy as np
    cv2.setNumThreads(1)
    cv2.ocl.setUseOpenCL(False)
    width, height = request["image_width"], request["image_height"]
    if not isinstance(width, int) or not isinstance(height, int) or not 1 <= width <= 4096 or not 1 <= height <= 4096 or width * height > 4_000_000:
        raise ValueError("image bounds")
    raw = base64.b64decode(request["rgb"], validate=True)
    if len(raw) != width * height * 3:
        raise ValueError("image length")
    rgb = np.frombuffer(raw, dtype=np.uint8).reshape(height, width, 3)
    face, reason = locate_face(rgb, detector)
    if reason:
        return None, reason
    return contextual_tensor(rgb, face), ""


def analyze_image(request, detector):
    import base64
    import cv2
    import math
    import numpy as np
    width, height = request["image_width"], request["image_height"]
    if not isinstance(width, int) or not isinstance(height, int) or not 1 <= width <= 4096 or not 1 <= height <= 4096 or width * height > 4_000_000:
        raise ValueError("analysis image bounds")
    raw = base64.b64decode(request["rgb"], validate=True)
    if len(raw) != width * height * 3:
        raise ValueError("analysis image length")
    rgb = np.frombuffer(raw, dtype=np.uint8).reshape(height, width, 3)
    face, reason = locate_face(rgb, detector)
    if reason:
        return [reason]
    x, y, w, h, landmarks = face
    codes = []
    if landmarks.shape != (5, 2) or not np.isfinite(landmarks).all():
        codes.append("landmarks_invalid")
    else:
        eye_distance = float(np.linalg.norm(landmarks[0] - landmarks[1]))
        if eye_distance < max(8, w * 0.12):
            codes.append("landmarks_invalid")
        else:
            eye_delta = landmarks[1] - landmarks[0]
            roll = abs(math.degrees(math.atan2(float(eye_delta[1]), float(eye_delta[0]))))
            eye_midpoint = (landmarks[0] + landmarks[1]) / 2
            yaw_ratio = abs(float(landmarks[2][0] - eye_midpoint[0])) / eye_distance
            if roll > 15 or yaw_ratio > 0.22:
                codes.append("pose_out_of_range")
    gray = cv2.cvtColor(rgb, cv2.COLOR_RGB2GRAY)
    brightness, contrast = float(gray.mean()), float(gray.std())
    sharpness = float(cv2.Laplacian(gray, cv2.CV_64F).var())
    glare = float(np.mean(np.all(rgb >= 245, axis=2)))
    if brightness < 40 or brightness > 220:
        codes.append("brightness_out_of_range")
    if contrast < 20:
        codes.append("contrast_too_low")
    if sharpness < 50:
        codes.append("sharpness_too_low")
    if glare > 0.10:
        codes.append("glare_too_high")
    return codes


def matching_tensor(request, detector):
    import base64
    import cv2
    import numpy as np
    width, height = request["image_width"], request["image_height"]
    if not isinstance(width, int) or not isinstance(height, int) or not 1 <= width <= 4096 or not 1 <= height <= 4096 or width * height > 4_000_000:
        raise ValueError("matching image bounds")
    raw = base64.b64decode(request["rgb"], validate=True)
    if len(raw) != width * height * 3:
        raise ValueError("matching image length")
    rgb = np.frombuffer(raw, dtype=np.uint8).reshape(height, width, 3)
    face, reason = locate_face(rgb, detector)
    if reason:
        return None, reason
    # Select exactly one portrait from the whole input (including a document
    # image), then align it to the embedding model's pinned ArcFace geometry.
    x, y, w, h, landmarks = face
    if landmarks.shape != (5, 2) or not np.isfinite(landmarks).all() or np.linalg.norm(landmarks[0] - landmarks[1]) < max(8, w * 0.12):
        return None, "landmarks_invalid"
    if np.any(landmarks[:, 0] < x - w * 0.2) or np.any(landmarks[:, 0] > x + w * 1.2) or np.any(landmarks[:, 1] < y - h * 0.2) or np.any(landmarks[:, 1] > y + h * 1.2):
        return None, "landmarks_invalid"
    target = np.asarray([[38.2946, 51.6963], [73.5318, 51.5014], [56.0252, 71.7366],
                         [41.5493, 92.3655], [70.7299, 92.2041]], dtype=np.float64)
    source_mean, target_mean = landmarks.mean(axis=0), target.mean(axis=0)
    source_centered, target_centered = landmarks - source_mean, target - target_mean
    covariance = target_centered.T @ source_centered / landmarks.shape[0]
    u, singular, vt = np.linalg.svd(covariance)
    direction = np.ones(2, dtype=np.float64)
    if np.linalg.det(covariance) < 0:
        direction[-1] = -1
    rotation = u @ np.diag(direction) @ vt
    variance = np.mean(np.sum(source_centered * source_centered, axis=1))
    if not np.isfinite(variance) or variance < 1e-9:
        return None, "landmarks_invalid"
    scale = float(np.dot(singular, direction) / variance)
    transform = np.concatenate(((scale * rotation), (target_mean - scale * rotation @ source_mean)[:, None]), axis=1)
    aligned = cv2.warpAffine(rgb, transform, (112, 112), flags=cv2.INTER_LINEAR, borderMode=cv2.BORDER_CONSTANT)
    return (aligned.transpose(2, 0, 1)[None].astype(np.float32) - 127.5) / 127.5, ""


def match_pair(request, detector, session, runtime_digest):
    import numpy as np
    embeddings = []
    for role in ("document", "selfie"):
        tensor, reason = matching_tensor(request[role], detector)
        if reason:
            return {"runtime_digest": runtime_digest, "reason": role + "_" + reason}
        value = session.run(["output"], {"input": tensor})[0]
        if value.shape != (1, 512) or not np.isfinite(value).all():
            raise ValueError("embedding schema")
        value = value[0].astype(np.float64)
        norm = np.linalg.norm(value)
        if not np.isfinite(norm) or norm < 1e-12:
            raise ValueError("degenerate embedding")
        embeddings.append(value / norm)
    score = float(np.dot(embeddings[0], embeddings[1]))
    if not np.isfinite(score):
        raise ValueError("similarity")
    return {"runtime_digest": runtime_digest, "score": float(np.clip(score, -1, 1))}
