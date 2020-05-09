/**
 * @fileoverview Component to manage Kubeflow application UIs within an iframe.
 */
import {html, PolymerElement} from '@polymer/polymer';

import {
    IFRAME_CONNECTED_EVENT,
    MESSAGE,
    NAMESPACE_SELECTED_EVENT,
    PARENT_CONNECTED_EVENT,
} from '../library.js';

export class IframeContainer extends PolymerElement {
    static get template() {
        return html`
            <style>
                :host {
                    flex: 1;
                }

                iframe {
                    border: 0;
                    display: inline-block;
                    height: 100%;
                    width: 100%;
                }
            </style>
            <iframe id="iframe"></iframe>
        `;
    }

    static get properties() {
        return {
            namespace: {type: String, observer: '_sendNamespaceMessage'},
            src: {type: String, observer: '_srcChanged'},
            page: {type: String, notify: true},
        };
    }

    /**
     * Initializes private iframe state variables and attaches a listener for
     * messages received by the window object.
     */
    ready() {
        super.ready();
        this._iframeOrigin = null;
        this._messageListener = this._onMessageReceived.bind(this);
        window.addEventListener(MESSAGE, this._messageListener);

        // Adds a click handler to be able to capture navigation events from
        // the captured iframe and set the page property which notifies
        const iframe = this.$.iframe;
        iframe.addEventListener('load', () => {
            iframe.contentDocument.addEventListener('click', () => {
                const iframeLocation = iframe.contentWindow.location;
                const newIframePage = iframeLocation.href.slice(
                    iframeLocation.origin.length);
                if (this.page !== newIframePage) {
                    this.page = newIframePage;
                }
            });
        });
    }

    /**
     * Remove the event listener for messages.
     */
    disconnectedCallback() {
        super.disconnectedCallback();
        window.removeEventListener(MESSAGE, this._messageListener);
    }

    /**
     * Programmatically sets the iframe's src when the property changes.
     * @param {string} newSrc
     */
    _srcChanged(newSrc) {
        const iframe = this.$.iframe;
        if (iframe.contentWindow.location.toString() !== newSrc) {
            iframe.contentWindow.location.replace(newSrc);
        }
    }

    /**
     * Sends a message to the iframe message bus. This is used on the iframe
     * load event as well as when the namespace changes.
     */
    _sendNamespaceMessage() {
        if (!(this._iframeOrigin && this.namespace)) return;
        this.$.iframe.contentWindow.postMessage({
            type: NAMESPACE_SELECTED_EVENT,
            value: this.namespace,
        }, this._iframeOrigin);
    }

    /**
     * Receives a message from an iframe page and passes the selected namespace.
     * @param {MessageEvent} event
     */
    _onMessageReceived(event) {
        const {data, origin} = event;
        this._iframeOrigin = origin;
        switch (data.type) {
        case IFRAME_CONNECTED_EVENT:
            this.$.iframe.contentWindow.postMessage({
                type: PARENT_CONNECTED_EVENT,
                value: null,
            }, origin);
            this._sendNamespaceMessage();
            break;
        }
    }
}

window.customElements.define('iframe-container', IframeContainer);
