import datetime
import logging
import os
import subprocess
import tempfile
import uuid
from retrying import retry

import pytest

from kubeflow.testing import util
from testing import deploy_utils
from testing import gcp_util

# TODO(https://github.com/kubeflow/kfctl/issues/42):
# Test is failing pretty consistently.
@pytest.mark.xfail
# There's really no good reason to run test_endpoint during presubmits.
# We shouldn't need it to feel confident that kfctl is working.
@pytest.mark.skipif(os.getenv("JOB_TYPE") == "presubmit",
                    reason="test endpoint doesn't run in presubmits")
def test_endpoint_is_ready(record_xml_attribute, project, app_name):
  """Test that Kubeflow was successfully deployed.

  Args:
    project: The gcp project that we deployed kubeflow
    app_name: The name of the kubeflow deployment
  """
  util.set_pytest_junit(record_xml_attribute, "test_endpoint_is_ready")

  # Owned by project kubeflow-ci-deployment.
  os.environ["CLIENT_ID"] = "29647740582-7meo6c7a9a76jvg54j0g2lv8lrsb4l8g.apps.googleusercontent.com"
  if not gcp_util.endpoint_is_ready(
      "https://{}.endpoints.{}.cloud.goog".format(app_name, project),
      wait_min=25):
    raise Exception("Endpoint not ready")

if __name__ == "__main__":
  logging.basicConfig(level=logging.INFO,
                      format=('%(levelname)s|%(asctime)s'
                              '|%(pathname)s|%(lineno)d| %(message)s'),
                      datefmt='%Y-%m-%dT%H:%M:%S',
                      )
  logging.getLogger().setLevel(logging.INFO)
  pytest.main()
