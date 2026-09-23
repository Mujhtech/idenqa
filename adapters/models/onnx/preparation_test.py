"""Image geometry and detector rejection tests; all pixels are synthetic."""
import unittest
import cv2
import numpy as np
from preparation import analyze_image, contextual_tensor, locate_face


class Output:
    def __init__(self, name):
        self.name = name


class Detector:
    def __init__(self, boxes=(), invalid=False):
        self.values = {}
        for kind, size in (("cls", 1), ("obj", 1), ("bbox", 4), ("kps", 10)):
            for stride in (8, 16, 32):
                self.values[f"{kind}_{stride}"] = np.zeros((1, (640 // stride) ** 2, size), dtype=np.float32)
        for index, (x, y, w, h) in enumerate(boxes):
            self.values["cls_8"][0, index] = 0.99
            self.values["obj_8"][0, index] = 0.99
            self.values["bbox_8"][0, index] = [(x+w/2)/8-index, (y+h/2)/8, np.log(w/8), np.log(h/8)]
            points = ((x + 0.32*w, y + 0.38*h), (x + 0.68*w, y + 0.38*h),
                      (x + 0.50*w, y + 0.56*h), (x + 0.37*w, y + 0.76*h),
                      (x + 0.63*w, y + 0.76*h))
            self.values["kps_8"][0, index] = [coordinate for px, py in points
                                                for coordinate in (px/8-index, py/8)]
        if invalid:
            self.values["bbox_8"][0, 0, 0] = np.nan

    def get_outputs(self):
        return [Output(name) for name in self.values]

    def run(self, _, inputs):
        assert inputs["input"].shape == (1,3,640,640)
        return list(self.values.values())


class PreparationTest(unittest.TestCase):
    def test_rejections(self):
        for boxes, reason in [([], "face_not_found"), ([(100,100,100,100),(400,400,100,100)], "multiple_faces"), ([(100,100,20,20)], "face_too_small"), ([(0,100,100,100)], "face_at_edge")]:
            with self.subTest(reason=reason):
                face, actual = locate_face(np.zeros((640,640,3),dtype=np.uint8),Detector(boxes))
                self.assertIsNone(face)
                self.assertEqual(actual,reason)
        with self.assertRaises(ValueError):
            locate_face(np.zeros((640,640,3),dtype=np.uint8),Detector(invalid=True))

    def test_context_contains_surroundings_and_reflects_edges(self):
        # Face begins at x=5. Its 1.5x crop begins outside the image at x=-11.
        rgb=np.zeros((160,160,3),dtype=np.uint8)
        rgb[:,:]=[10,20,30]
        rgb[5:69,5:69]=[200,100,50]
        actual=contextual_tensor(rgb,(5,5,64,64))
        expected=cv2.copyMakeBorder(rgb[:85,:85],11,0,11,0,cv2.BORDER_REFLECT_101)
        expected=cv2.resize(expected,(128,128),interpolation=cv2.INTER_LANCZOS4).transpose(2,0,1)[None].astype(np.float32)/255
        np.testing.assert_array_equal(actual,expected)
        self.assertGreater(actual[0,0,64,64],0.7)
        self.assertLess(actual[0,0,-1,-1],0.1)

    def test_area_downsampling(self):
        rgb=np.arange(300*300*3,dtype=np.uint8).reshape(300,300,3)
        actual=contextual_tensor(rgb,(60,60,160,160))
        expected=cv2.resize(rgb[20:260,20:260],(128,128),interpolation=cv2.INTER_AREA).transpose(2,0,1)[None].astype(np.float32)/255
        np.testing.assert_array_equal(actual,expected)

    def test_analysis_returns_only_bounded_classifications(self):
        import base64
        rgb = np.zeros((640,640,3),dtype=np.uint8)
        request = {"rgb":base64.b64encode(rgb.tobytes()).decode(),"image_width":640,"image_height":640}
        codes = analyze_image(request, Detector([(100,100,200,200)]))
        self.assertIn("brightness_out_of_range", codes)
        self.assertIn("contrast_too_low", codes)
        self.assertIn("sharpness_too_low", codes)
        self.assertNotIn("face_not_found", codes)

    def test_analysis_stops_when_face_selection_is_ambiguous(self):
        import base64
        rgb = np.zeros((640,640,3),dtype=np.uint8)
        request = {"rgb":base64.b64encode(rgb.tobytes()).decode(),"image_width":640,"image_height":640}
        self.assertEqual(analyze_image(request, Detector([])), ["face_not_found"])
        self.assertEqual(analyze_image(request, Detector([(100,100,100,100),(400,400,100,100)])), ["multiple_faces"])

if __name__ == "__main__":
    unittest.main()

class MatchingTest(unittest.TestCase):
    def test_portrait_crop_and_normalization(self):
        import base64
        from preparation import matching_tensor
        rgb = np.zeros((640,640,3),dtype=np.uint8)
        rgb[100:300,100:300] = [255,127,0]
        request = {"rgb":base64.b64encode(rgb.tobytes()).decode(),"image_width":640,"image_height":640}
        tensor,reason = matching_tensor(request,Detector([(100,100,200,200)]))
        self.assertEqual(reason, "")
        self.assertEqual(tensor.shape, (1,3,112,112))
        np.testing.assert_allclose(tensor[0,:,56,56], [1, (127-127.5)/127.5, -1], atol=1e-6)

    def test_degenerate_embeddings_and_quality(self):
        import base64
        from preparation import match_pair
        picture = {"rgb":base64.b64encode(bytes(640*640*3)).decode(),"image_width":640,"image_height":640}
        request = {"document":picture,"selfie":picture}
        class Embedding:
            def __init__(self,value): self.value=value
            def run(self,*args): return [self.value]
        for value in [np.zeros((1,512),dtype=np.float32),np.full((1,512),np.nan,dtype=np.float32),np.ones((1,2),dtype=np.float32)]:
            with self.subTest(shape=value.shape):
                with self.assertRaises(ValueError):
                    match_pair(request,Detector([(100,100,200,200)]),Embedding(value),"fixture")
        result=match_pair(request,Detector([]),Embedding(None),"fixture")
        self.assertEqual(result["reason"],"document_face_not_found")
        self.assertNotIn("score",result)
