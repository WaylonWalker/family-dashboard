import unittest
from pin_image import pin


class PinImageTest(unittest.TestCase):
    def setUp(self):
        self.original = """apiVersion: apps/v1
spec:
  containers:
      image: ghcr.io/waylonwalker/family-dashboard:sha-REPLACE_BEFORE_SYNC
"""

    def test_replaces_exactly_one_image(self):
        new = pin(self.original, "ghcr.io/waylonwalker/family-dashboard:sha-deadbeef")
        self.assertIn("family-dashboard:sha-deadbeef", new)

    def test_accepts_digest_pinned_image(self):
        digest = "a" * 64
        new = pin(self.original, f"ghcr.io/waylonwalker/family-dashboard:sha-deadbeef@sha256:{digest}")
        self.assertIn(f"sha-deadbeef@sha256:{digest}", new)

    def test_rejects_invalid_digest(self):
        with self.assertRaises(ValueError):
            pin(self.original, "ghcr.io/waylonwalker/family-dashboard:sha-deadbeef@sha256:bad")

    def test_rejects_mutually_ambiguous_images(self):
        with self.assertRaises(ValueError):
            pin(self.original + "      image: other:tag\n", "ghcr.io/waylonwalker/family-dashboard:sha-deadbeef")

    def test_rejects_registry_not_allowlisted(self):
        with self.assertRaises(ValueError):
            pin(self.original, "docker.io/unknown/app:latest")


if __name__ == "__main__":
    unittest.main()
