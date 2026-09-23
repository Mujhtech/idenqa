"""Reproducible runtime fixture; not a trained PAD model or assurance evidence."""
from pathlib import Path
import onnx
from onnx import TensorProto, helper

source = helper.make_tensor_value_info("input", TensorProto.FLOAT, ["batch_size", 3, 128, 128])
output = helper.make_tensor_value_info("output", TensorProto.FLOAT, ["batch_size", 2])
axis = helper.make_tensor("axis", TensorProto.INT64, [1], [1])
graph = helper.make_graph([
    helper.make_node("ReduceMean", ["input"], ["mean"], axes=[1, 2, 3], keepdims=0),
    helper.make_node("Unsqueeze", ["mean", "axis"], ["real"]),
    helper.make_node("Neg", ["real"], ["spoof"]),
    helper.make_node("Concat", ["real", "spoof"], ["output"], axis=1),
], "idenqa-pad-runtime-fixture", [source], [output], [axis])
model = helper.make_model(graph, producer_name="idenqa.fixture", opset_imports=[helper.make_opsetid("", 13)], ir_version=8)
onnx.checker.check_model(model)
Path(__file__).with_name("pad_fixture.onnx").write_bytes(model.SerializeToString(deterministic=True))

# A constant detector fixture exercises the native crop path without a face dataset.
import numpy as np
from onnx import numpy_helper
outputs, nodes = [], []
for kind, size in (("cls", 1), ("obj", 1), ("bbox", 4), ("kps", 10)):
    for stride in (8, 16, 32):
        name = f"{kind}_{stride}"
        values = np.zeros((1, (640 // stride) ** 2, size), dtype=np.float32)
        if stride == 8:
            index = 40 * 80 + 40
            if kind in ("cls", "obj"):
                values[0, index, 0] = 0.99
            if kind == "bbox":
                values[0, index, 2:] = np.log(50)
            if kind == "kps":
                # Five non-degenerate points within the synthetic 400x400 box.
                points = ((248, 272), (392, 272), (320, 344), (268, 424), (372, 424))
                values[0, index] = [coordinate for px, py in points
                                    for coordinate in (px / 8 - 40, py / 8 - 40)]
        outputs.append(helper.make_tensor_value_info(name, TensorProto.FLOAT, list(values.shape)))
        nodes.append(helper.make_node("Constant", [], [name], value=numpy_helper.from_array(values)))
graph = helper.make_graph(nodes, "idenqa-detector-runtime-fixture", [helper.make_tensor_value_info("input", TensorProto.FLOAT, [1,3,640,640])], outputs)
model = helper.make_model(graph, producer_name="idenqa.fixture", opset_imports=[helper.make_opsetid("",13)], ir_version=8)
onnx.checker.check_model(model)
Path(__file__).with_name("detector_fixture.onnx").write_bytes(model.SerializeToString(deterministic=True))

# Synthetic embedding graph, not a face-recognition model. Input colour drives
# a nonzero 512-vector to exercise pair execution and cosine normalization.
source = helper.make_tensor_value_info("input", TensorProto.FLOAT, [1, 3, 112, 112])
output = helper.make_tensor_value_info("output", TensorProto.FLOAT, [1, 512])
axis = helper.make_tensor("axis", TensorProto.INT64, [1], [1])
weights = numpy_helper.from_array(np.linspace(0.25, 1.0, 512, dtype=np.float32).reshape(1,512), "weights")
bias = numpy_helper.from_array(np.ones((1,512), dtype=np.float32), "bias")
graph = helper.make_graph([
    helper.make_node("ReduceMean", ["input"], ["mean"], axes=[1,2,3], keepdims=0),
    helper.make_node("Unsqueeze", ["mean", "axis"], ["scalar"]),
    helper.make_node("Mul", ["scalar", "weights"], ["scaled"]),
    helper.make_node("Add", ["scaled", "bias"], ["output"]),
], "idenqa-embedding-runtime-fixture", [source], [output], [axis, weights, bias])
model = helper.make_model(graph, producer_name="idenqa.fixture", opset_imports=[helper.make_opsetid("",13)], ir_version=8)
onnx.checker.check_model(model)
Path(__file__).with_name("embedding_fixture.onnx").write_bytes(model.SerializeToString(deterministic=True))
