import os
import sys
import logging
from flask_cors import CORS
from kubeflow_jupyter.default.app import app as default
from kubeflow_jupyter.rok.app import app as rok

logger = logging.getLogger("entrypoint")

# Get the UIs
ui = os.environ.get("UI", "default")
apps = {
    "default": default,
    "rok": rok
}

try:
    app = apps[ui]

    # Enable CORS for dev
    if "--enable-cors" in sys.argv:
        logger.warning("Enabling CORS")
        CORS(app)

    app.run(host="0.0.0.0")
except KeyError:
    logger.warning("There is no " + ui + " UI to load.")
    exit(1)
