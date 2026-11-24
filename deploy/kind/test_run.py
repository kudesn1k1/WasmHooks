import re
import unittest

import run


class Provisioning(unittest.TestCase):
    def test_generated_secrets_match_between_services(self):
        values = run.platform_credentials()
        self.assertEqual(values["CP_INTERNAL_TOKEN"], values["WASMHOOKS_INTERNAL_TOKEN"])
        self.assertEqual(values["CP_S3_SECRET_KEY"], values["MINIO_ROOT_PASSWORD"])
        self.assertEqual(values["CP_S3_SECRET_KEY"], values["WASMHOOKS_S3_SECRET_KEY"])
        self.assertIn(values["POSTGRES_PASSWORD"], values["CP_DATABASE_URL"])
        self.assertGreaterEqual(len(values["CP_TENANT_JWT_SECRET"]), 32)
        self.assertNotEqual(values, run.platform_credentials())

    def test_stack_routes_snapshot_validation_and_shop_to_correct_roles(self):
        manifest = (run.HERE / "stack.yaml").read_text(encoding="utf-8")
        for setting in ("http://executors:8081", "http://gateway:8080", "-role=executor",
                        "-role=gateway", "-executor-address=$(POD_IP):9090", "replicas: 3",
                        "clusterIP: None", "m3-installation"):
            self.assertIn(setting, manifest)
        self.assertEqual(len(re.findall(r"kind: Deployment", manifest)), 4)


if __name__ == "__main__":
    unittest.main()
