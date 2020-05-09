import '@polymer/iron-ajax/iron-ajax.js';
import '@polymer/iron-icons/iron-icons.js';
import '@polymer/iron-media-query/iron-media-query.js';
import '@polymer/paper-card/paper-card.js';
import '@polymer/iron-a11y-keys/iron-a11y-keys.js';
import 'web-animations-js/web-animations-next.min.js';
import '@polymer/neon-animation/neon-animatable.js';
import '@polymer/neon-animation/neon-animated-pages.js';
import '@polymer/neon-animation/animations/fade-in-animation.js';
import '@polymer/neon-animation/animations/fade-out-animation.js';

import {html, PolymerElement} from '@polymer/polymer/polymer-element.js';

import './resources/md2-input/md2-input.js';
import './resources/carousel-indicator.js';
import './resources/animated-checkmark.js';
import css from './registration-page.css';
import template from './registration-page.pug';
import logo from '../assets/logo.svg';

import utilitiesMixin from './utilities-mixin.js';

/**
 * Entry point for application UI.
 */
export class RegistrationPage extends utilitiesMixin(PolymerElement) {
    static get template() {
        const vars = {logo};
        return html([
            `<style>${css.toString()}</style>${template(vars)}`]);
    }

    static get properties() {
        return {
            userDetails: {type: Object, observer: '_onUserDetails'},
            namespaceInput: {type: Object},
            page: {type: Number, value: 0},
            namespaceName: String,
            error: Object,
            flowComplete: {type: Boolean, value: false},
            waitForRedirect: {type: Boolean, value: false},
            showAPIText: {type: Boolean, value: false},
            _namespaceValidationRegex: {
                type: String,
                readOnly: true,
                // eslint-disable-next-line
                value: '[-a-z0-9\.]',
            },
        };
    }

    ready() {
        super.ready();
        this.namespaceInput = this.$.Namespace;
    }

    _onUserDetails(d) {
        this.namespaceName = this.userDetails
            // eslint-disable-next-line no-useless-escape
            .replace(/[^\w]|\./g, '-')
            .replace(/^-+|-+$|_/g, '')
            .toLowerCase();
    }

    clearInvalidation() {
        this.namespaceInput.invalid = false;
    }

    nextPage() {
        this.page++;
    }

    backPage() {
        this.page--;
    }

    showError(msg) {
        this.set('error', {response: {error: msg}});
    }

    validateNamespace() {
        const finalRgx = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
        if (finalRgx.test(this.namespaceName)) return true;
        this.showError(
            `Name can only start and end with alpha-num characters, `+
            `dashes are only permitted between start and end. (minlength >= 1)`
        );
    }

    async finishSetup() {
        const API = this.$.MakeNamespace;
        if (!this.validateNamespace()) return;
        API.body = {namespace: this.namespaceName};
        this.waitForRedirect = true;
        await API.generateRequest().completes;
        await this.sleep(500);
        if (this.error && this.error.response) {
            return this.waitForRedirect = false;
        }
        // If request completed and 6 seconds pass, probably let
        // the user click next again!
        await this.sleep(6000);
        this.waitForRedirect = false;
    }

    _successSetup() {
        this.flowComplete = true;
        this.set('error', {});
        this.fireEvent('flowcomplete');
    }
}

window.customElements.define('registration-page', RegistrationPage);
