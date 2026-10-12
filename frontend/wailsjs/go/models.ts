export namespace engine {
	
	export class FileStatus {
	    index: number;
	    path: string;
	    size: number;
	    done: number;
	    wanted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.path = source["path"];
	        this.size = source["size"];
	        this.done = source["done"];
	        this.wanted = source["wanted"];
	    }
	}
	export class Status {
	    hash: string;
	    name: string;
	    dir: string;
	    state: string;
	    size: number;
	    done: number;
	    progress: number;
	    downRate: number;
	    upRate: number;
	    peers: number;
	    seeds: number;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.name = source["name"];
	        this.dir = source["dir"];
	        this.state = source["state"];
	        this.size = source["size"];
	        this.done = source["done"];
	        this.progress = source["progress"];
	        this.downRate = source["downRate"];
	        this.upRate = source["upRate"];
	        this.peers = source["peers"];
	        this.seeds = source["seeds"];
	    }
	}

}

export namespace main {
	
	export class LoginResult {
	    display: string;
	    needField?: vpnprov.Field;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new LoginResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.display = source["display"];
	        this.needField = this.convertValues(source["needField"], vpnprov.Field);
	        this.message = source["message"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Settings {
	    downloadDir: string;
	    vpnProvider: string;
	    forwardedPort: number;
	    accountProvider: string;
	    accountRegion: string;
	    downLimitKB: number;
	    upLimitKB: number;
	    closeToTray: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.downloadDir = source["downloadDir"];
	        this.vpnProvider = source["vpnProvider"];
	        this.forwardedPort = source["forwardedPort"];
	        this.accountProvider = source["accountProvider"];
	        this.accountRegion = source["accountRegion"];
	        this.downLimitKB = source["downLimitKB"];
	        this.upLimitKB = source["upLimitKB"];
	        this.closeToTray = source["closeToTray"];
	    }
	}
	export class SettingsView {
	    settings: Settings;
	    customEndpoint: string;
	    assoc: platform.AssocStatus;
	    accounts: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.settings = this.convertValues(source["settings"], Settings);
	        this.customEndpoint = source["customEndpoint"];
	        this.assoc = this.convertValues(source["assoc"], platform.AssocStatus);
	        this.accounts = source["accounts"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class VpnStatus {
	    state: string;
	    provider: string;
	    name: string;
	    server: string;
	    exitIP: string;
	    location: string;
	    colo: string;
	    port: number;
	    warning: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new VpnStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.provider = source["provider"];
	        this.name = source["name"];
	        this.server = source["server"];
	        this.exitIP = source["exitIP"];
	        this.location = source["location"];
	        this.colo = source["colo"];
	        this.port = source["port"];
	        this.warning = source["warning"];
	        this.error = source["error"];
	    }
	}

}

export namespace platform {
	
	export class AssocStatus {
	    supported: boolean;
	    magnet: boolean;
	    torrent: boolean;
	    canOpenSettings: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AssocStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supported = source["supported"];
	        this.magnet = source["magnet"];
	        this.torrent = source["torrent"];
	        this.canOpenSettings = source["canOpenSettings"];
	    }
	}

}

export namespace vpnprov {
	
	export class Field {
	    key: string;
	    label: string;
	    kind: string;
	    help?: string;
	
	    static createFrom(source: any = {}) {
	        return new Field(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.kind = source["kind"];
	        this.help = source["help"];
	    }
	}
	export class Info {
	    id: string;
	    name: string;
	    fields: Field[];
	    portForwarding: boolean;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.fields = this.convertValues(source["fields"], Field);
	        this.portForwarding = source["portForwarding"];
	        this.note = source["note"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Region {
	    id: string;
	    name: string;
	    portForward: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Region(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.portForward = source["portForward"];
	    }
	}

}

