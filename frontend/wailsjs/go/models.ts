export namespace engine {
	
	export class Status {
	    hash: string;
	    name: string;
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
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.downloadDir = source["downloadDir"];
	    }
	}
	export class VpnStatus {
	    state: string;
	    exitIP: string;
	    location: string;
	    colo: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new VpnStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.exitIP = source["exitIP"];
	        this.location = source["location"];
	        this.colo = source["colo"];
	        this.error = source["error"];
	    }
	}

}

