import '@polymer/app-layout/app-drawer/app-drawer.js';
import '@polymer/app-layout/app-drawer-layout/app-drawer-layout.js';
import '@polymer/app-layout/app-header/app-header.js';
import '@polymer/app-layout/app-header-layout/app-header-layout.js';
import '@polymer/app-layout/app-scroll-effects/app-scroll-effects.js';
import '@polymer/app-layout/app-toolbar/app-toolbar.js';
import '@polymer/app-route/app-location.js';
import '@polymer/app-route/app-route.js';
import '@polymer/iron-ajax/iron-ajax.js';
import '@polymer/iron-icons/iron-icons.js';
import '@polymer/iron-image/iron-image.js';
import '@polymer/iron-collapse/iron-collapse.js';
import '@polymer/iron-selector/iron-selector.js';
import '@polymer/iron-media-query/iron-media-query.js';
import '@polymer/paper-card/paper-card.js';
import '@polymer/paper-tabs/paper-tabs.js';
import '@polymer/paper-item/paper-item.js';
import '@polymer/paper-item/paper-icon-item.js';
import '@polymer/paper-item/paper-item-body.js';
import '@polymer/paper-dropdown-menu/paper-dropdown-menu.js';
import 'web-animations-js/web-animations-next.min.js';
import '@polymer/neon-animation/neon-animatable.js';
import '@polymer/neon-animation/neon-animated-pages.js';
import '@polymer/neon-animation/animations/fade-in-animation.js';
import '@polymer/neon-animation/animations/fade-out-animation.js';

import {html, PolymerElement} from '@polymer/polymer/polymer-element.js';

import css from './main-page.css';
import template from './main-page.pug';
import logo from '../assets/logo.svg';
import '../assets/anon-user.png';

import './registration-page.js';
import './namespace-selector.js';
import './dashboard-view.js';
import './activity-view.js';
import './not-found-view.js';
import './manage-users-view.js';
import './resources/kubeflow-icons.js';
import './iframe-container.js';
import utilitiesMixin from './utilities-mixin.js';
import {IFRAME_LINK_PREFIX} from './iframe-link.js';

/**
 * Entry point for application UI.
 */
export class MainPage extends utilitiesMixin(PolymerElement) {
    static get template() {
        const vars = {logo};
        return html([
            `<style>${css.toString()}</style>${template(vars)}`]);
    }

    static get properties() {
        return {
            page: String,
            routeData: Object,
            subRouteData: Object,
            queryParams: {
                type: Object,
                value: null, // Necessary to preserve queryString from load
            },
            iframeSrc: String,
            iframePage: {type: String, observer: '_iframePageChanged'},
            menuLinks: {
                type: Array,
                value: [
                    {
                        link: '/pipeline/',
                        text: '工作流',
                    },
                    {
                        link: '/jupyter/',
                        text: 'Notebook服务',
                    },
                    /*{
                        link: '/katib/',
                        text: 'Katib',
                    },
                    {
                        link: '/metadata/',
                        text: 'Artifact Store',
                    },*/
                ],
            },
            sidebarItemIndex: {
                type: Number,
                value: 0,
                observer: '_revertSidebarIndexIfExternal',
            },
            errorText: {type: String, value: ''},
            buildVersion: {type: String, value: BUILD_VERSION},
            dashVersion: {type: String, value: VERSION},
            platformInfo: Object,
            inIframe: {type: Boolean, value: false, readOnly: true},
            hideTabs: {type: Boolean, value: false, readOnly: true},
            hideSidebar: {type: Boolean, value: false, readOnly: true},
            hideNamespaces: {type: Boolean, value: false, readOnly: true},
            allNamespaces: {type: Boolean, value: false, readOnly: true},
            notFoundInIframe: {type: Boolean, value: false, readOnly: true},
            registrationFlow: {type: Boolean, value: false, readOnly: true},
            workgroupStatusHasLoaded: {
                type: Boolean,
                value: false,
                readOnly: true,
            },
            namespaces: Array,
            namespace: String,
            user: String,
            isClusterAdmin: {type: Boolean, value: false},
            isolationMode: {type: String, value: 'undecided', readOnly: true},
            _shouldFetchEnv: {
                type: Boolean,
                // eslint-disable-next-line max-len
                computed: 'computeShouldFetchEnv(registrationFlow, workgroupStatusHasLoaded)',
            },
        };
    }

    /**
     * Array of strings describing multi-property observer methods and their
     * dependant properties
     */
    static get observers() {
        return [
            '_routePageChanged(routeData.page)',
        ];
    }

    /**
     * Return a username without the @example.com
     * @param {string} user User email
     * @return {string} User Name.
     */
    _extractLdap(user) {
        return user.replace(/@.*$/, '');
    }

    /**
     * Resync the app with environment information
     */
    async resyncApp() {
        await this.$.envInfo.generateRequest().completes;
        await this.sleep(100);
        this.$.welcomeUser.show();
    }

    /**
     * Show a toast error on main-page
     * @param {string} err Error message to show
     */
    showError(err) {
        this.errorText = err;
    }

    closeError() {
        this.errorText = '';
    }

    /**
     * Set state for loading registration flow in case no workgroup exists
     * @param {Event} ev AJAX-response
     */
    _onHasWorkgroupError(ev) {
        const error = ((ev.detail.request||{}).response||{}).error ||
            ev.detail.error;
        this.showError(error);
        return;
    }

    /**
     * Set state for loading registration flow in case no workgroup exists
     * @param {Event} ev AJAX-response
     */
    _onHasWorkgroupResponse(ev) {
        const {user, hasWorkgroup, hasAuth} = ev.detail.response;
        this._setIsolationMode(hasAuth ? 'multi-user' : 'single-user');
        if (hasAuth && !hasWorkgroup) {
            this.user = user;
            this._setRegistrationFlow(true);
        }
        this._setWorkgroupStatusHasLoaded(true);
    }

    /**
     * Handles route changes by evaluating the page path component
     * @param {string} newPage
     */
    _routePageChanged(newPage) {
        let isIframe = false;
        let notFoundInIframe = false;
        let hideTabs = true;
        let hideNamespaces = false;
        let allNamespaces = false;
        let hideSidebar = false;

        switch (newPage) {
        case 'activity':
            this.sidebarItemIndex = 0;
            this.page = 'activity';
            hideTabs = false;
            break;
        case IFRAME_LINK_PREFIX:
            this.page = 'iframe';
            isIframe = true;
            hideNamespaces = this.subRouteData.path.startsWith('/pipeline');
            this._setActiveMenuLink(this.subRouteData.path);
            this._setIframeSrc();
            break;
        case 'manage-users':
            this.sidebarItemIndex = 6;
            this.page = 'manage-users';
            hideTabs = true;
            allNamespaces = true;
            hideSidebar = true;
            break;
        case '':
            this.sidebarItemIndex = 0;
            this.page = 'dashboard';
            hideTabs = false;
            break;
        default:
            this.sidebarItemIndex = -1;
            this.page = 'not_found';
            // Handles case when an iframed page requests an invalid route
            if (this._isInsideOfIframe()) {
                notFoundInIframe = true;
            }
        }
        this._setNotFoundInIframe(notFoundInIframe);
        this._setHideTabs(hideTabs);
        this._setAllNamespaces(allNamespaces);
        this._setHideNamespaces(hideNamespaces);
        this._setInIframe(isIframe);
        this._setHideSidebar(hideSidebar);

        // If iframe <-> [non-frame OR other iframe]
        if (hideSidebar || isIframe !== this.inIframe || isIframe) {
            this.$.MainDrawer.close();
        }

        if (!isIframe) {
            this.iframeSrc = 'about:blank';
        }
    }

    /**
     * Builds the new iframeSrc string based on the subroute path, current
     * hash fragment, and the query string parameters other than ns.
     */
    _setIframeSrc() {
        const iframeUrl = new URL(this.subRouteData.path,
            window.location.origin);
        iframeUrl.hash = window.location.hash;
        iframeUrl.search = window.location.search;
        iframeUrl.searchParams.delete('ns');
        this.iframeSrc = iframeUrl.toString();
    }

    /**
     * Observer to reflect navigation in iframed pages and push to history.
     * @param {string} newPage - iframe page path
     */
    _iframePageChanged(newPage) {
        window.history.pushState(null, null,
            `/${IFRAME_LINK_PREFIX}${newPage}`);
    }

    /**
     * [ComputeProp] `shouldFetchEnv`
     * @param {boolean} registrationFlow
     * @param {boolean} workgroupStatusHasLoaded
     * @return {boolean}
     */
    computeShouldFetchEnv(registrationFlow, workgroupStatusHasLoaded) {
        return !registrationFlow && workgroupStatusHasLoaded;
    }

    /**
     * Revert the sidebar index if the item clicked is an external link
     * @param {int} curr
     * @param {int} old
     */
    _revertSidebarIndexIfExternal(curr, old=0) {
        if (curr <= this.menuLinks.length + 2) return;
        this.sidebarItemIndex = old;
    }

    /**
     * Tries to determine which menu link to activate based on the provided
     * path.
     * @param {string} path
     */
    _setActiveMenuLink(path) {
        const menuLinkIndex = this.menuLinks
            .findIndex((m) => path.startsWith(m.link));
        if (menuLinkIndex >= 0) {
            // Adds 1 since Overview is hard-coded
            this.sidebarItemIndex = menuLinkIndex + 1;
        } else {
            this.sidebarItemIndex = -1;
        }
    }

    /**
     * Returns true when this component is found to be iframed inside of a
     * parent page.
     * @return {boolean}
     */
    _isInsideOfIframe() {
        return window.location !== window.parent.location;
    }

    /**
     * Handles the AJAX response from the platform-info API.
     * @param {Event} responseEvent AJAX-response
     */
    _onEnvInfoResponse(responseEvent) {
        const {platform, user,
            namespaces, isClusterAdmin} = responseEvent.detail.response;
        Object.assign(this, {user, isClusterAdmin});
        this.namespaces = namespaces;
        if (this.namespaces.length) {
            this._setRegistrationFlow(false);
        } else if (this.isolationMode == 'single-user') {
            // This case is for non-identity networks, that have no namespaces
            this._setRegistrationFlow(true);
        }
        this.ownedNamespace = namespaces.find((n) => n.role == 'owner');
        this.platformInfo = platform;
        const kVer = this.platformInfo.kubeflowVersion;
        if (kVer && kVer != 'unknown') {
            this.buildVersion = this.platformInfo.kubeflowVersion;
        }
    }
}

window.customElements.define('main-page', MainPage);
