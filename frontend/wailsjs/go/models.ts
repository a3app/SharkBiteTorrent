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
	
	export class Settings {
	    downloadDir: string;
	    vpnProvider: string;
	    forwardedPort: number;
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
	        this.downLimitKB = source["downLimitKB"];
	        this.upLimitKB = source["upLimitKB"];
	        this.closeToTray = source["closeToTray"];
	    }
	}
	export class SettingsView {
	    settings: Settings;
	    customEndpoint: string;
	    assoc: winassoc.AssocStatus;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.settings = this.convertValues(source["settings"], Settings);
	        this.customEndpoint = source["customEndpoint"];
	        this.assoc = this.convertValues(source["assoc"], winassoc.AssocStatus);
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
	    exitIP: string;
	    location: string;
	    colo: string;
	    port: number;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new VpnStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.provider = source["provider"];
	        this.exitIP = source["exitIP"];
	        this.location = source["location"];
	        this.colo = source["colo"];
	        this.port = source["port"];
	        this.error = source["error"];
	    }
	}

}

export namespace winassoc {
	
	export class AssocStatus {
	    magnet: boolean;
	    torrent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AssocStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.magnet = source["magnet"];
	        this.torrent = source["torrent"];
	    }
	}

}

