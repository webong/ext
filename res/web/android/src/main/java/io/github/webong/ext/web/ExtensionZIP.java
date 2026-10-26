package io.github.webong.ext.web;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.zip.*;

/** Bounded classic ZIP codec. Central metadata is checked before decompression, including Unix file types. */
final class ExtensionZIP {
    private ExtensionZIP() {}
    private static long u(byte[] b,int at,int size) throws IOException {
        if(at<0||at>b.length-size)throw ExtensionArtifacts.invalid();long n=0;
        for(int i=0;i<size;i++)n|=(long)(b[at+i]&255)<<(8*i);return n;
    }
    private static String name(byte[] b,int at,int size) throws IOException {
        if(at<0||at>b.length-size)throw ExtensionArtifacts.invalid();byte[] bytes=Arrays.copyOfRange(b,at,at+size);
        String value=new String(bytes,StandardCharsets.UTF_8);if(!Arrays.equals(bytes,value.getBytes(StandardCharsets.UTF_8)))throw ExtensionArtifacts.invalid();return value;
    }
    static List<ExtensionArtifacts.Item> decode(byte[] b) throws IOException {
        int end=-1;
        for(int i=b.length-22;i>=Math.max(0,b.length-65557);i--)if(u(b,i,4)==0x06054b50L && i+22+u(b,i+20,2)==b.length){end=i;break;}
        if(end<0||u(b,end+4,2)!=0||u(b,end+6,2)!=0||u(b,end+8,2)!=u(b,end+10,2))throw ExtensionArtifacts.invalid();
        long count=u(b,end+10,2), central=u(b,end+16,4), centralSize=u(b,end+12,4);
        if(count>4096||central+centralSize!=end)throw ExtensionArtifacts.invalid();
        int at=(int)central;long total=0;List<ExtensionArtifacts.Item> result=new ArrayList<>();
        for(int i=0;i<count;i++) {
            if(u(b,at,4)!=0x02014b50L)throw ExtensionArtifacts.invalid();
            int flags=(int)u(b,at+8,2), method=(int)u(b,at+10,2), length=(int)u(b,at+28,2);
            long crc=u(b,at+16,4), packed=u(b,at+20,4), size=u(b,at+24,4), offset=u(b,at+42,4), attrs=u(b,at+38,4);
            long next=(long)at+46+length+u(b,at+30,2)+u(b,at+32,2);
            if(next>end||u(b,at+34,2)!=0||(flags&0x41)!=0||packed>ExtensionArtifacts.MAX_ZIP||size>ExtensionArtifacts.MAX_FILE)throw ExtensionArtifacts.invalid();
            String filename=name(b,at+46,length);String safe=filename.endsWith("/")?filename.substring(0,filename.length()-1):filename;
            ExtensionArtifacts.safeName(safe);
            int kind=(int)(attrs>>>16)&0170000;
            if((u(b,at+5,1)==3||u(b,at+5,1)==19)&&kind!=0&&kind!=0100000&&kind!=0040000)throw ExtensionArtifacts.invalid();
            boolean directory=filename.endsWith("/")||(attrs&16)!=0||kind==0040000;
            at=(int)next;if(directory)continue;
            if(result.size()>=ExtensionArtifacts.MAX_FILES||(total+=size)>ExtensionArtifacts.MAX_TOTAL||offset>central-30)throw ExtensionArtifacts.invalid();
            int local=(int)offset;
            if(u(b,local,4)!=0x04034b50L||u(b,local+6,2)!=flags||u(b,local+8,2)!=method)throw ExtensionArtifacts.invalid();
            int localLength=(int)u(b,local+26,2);long dataAt=offset+30+localLength+u(b,local+28,2);
            if(dataAt+packed>central||!name(b,local+30,localLength).equals(filename))throw ExtensionArtifacts.invalid();
            if((flags&8)==0&&(u(b,local+14,4)!=crc||u(b,local+18,4)!=packed||u(b,local+22,4)!=size))throw ExtensionArtifacts.invalid();
            byte[] data;
            if(method==0) {if(size!=packed)throw ExtensionArtifacts.invalid();data=Arrays.copyOfRange(b,(int)dataAt,(int)(dataAt+packed));}
            else if(method==8) {
                Inflater inflater=new Inflater(true);byte[] buffer=new byte[(int)size+1];
                try {
                    inflater.setInput(b,(int)dataAt,(int)packed);int written=0;
                    while(!inflater.finished()&&written<buffer.length) {
                        int n=inflater.inflate(buffer,written,buffer.length-written);if(n==0&&!inflater.finished())throw ExtensionArtifacts.invalid();written+=n;
                    }
                    if(!inflater.finished()||written!=size||inflater.getRemaining()!=0)throw ExtensionArtifacts.invalid();data=Arrays.copyOf(buffer,written);
                }catch(DataFormatException error){throw ExtensionArtifacts.invalid();}finally{inflater.end();}
            }else throw ExtensionArtifacts.invalid();
            CRC32 checksum=new CRC32();checksum.update(data);if(checksum.getValue()!=crc)throw ExtensionArtifacts.invalid();
            result.add(new ExtensionArtifacts.Item(filename,data));
        }
        if(at!=end)throw ExtensionArtifacts.invalid();return result;
    }
    private static void put(ByteArrayOutputStream out,long n,int size){for(int i=0;i<size;i++)out.write((int)(n>>>(i*8))&255);}
    static byte[] encode(List<ExtensionArtifacts.Item> files) throws IOException {
        ByteArrayOutputStream out=new ByteArrayOutputStream(), central=new ByteArrayOutputStream();
        for(ExtensionArtifacts.Item item:files) {
            byte[] name=item.name.getBytes(StandardCharsets.UTF_8);if(name.length>65535 || (long)out.size()+central.size()+76+name.length*2L+item.data.length+22>ExtensionArtifacts.MAX_ZIP)throw ExtensionArtifacts.invalid();
            CRC32 checksum=new CRC32();checksum.update(item.data);long crc=checksum.getValue();int offset=out.size();
            put(out,0x04034b50,4);put(out,20,2);put(out,0x800,2);put(out,0,2);put(out,0,2);put(out,33,2);
            put(out,crc,4);put(out,item.data.length,4);put(out,item.data.length,4);put(out,name.length,2);put(out,0,2);out.write(name);out.write(item.data);
            put(central,0x02014b50,4);put(central,0x314,2);put(central,20,2);put(central,0x800,2);put(central,0,2);put(central,0,2);put(central,33,2);
            put(central,crc,4);put(central,item.data.length,4);put(central,item.data.length,4);put(central,name.length,2);
            put(central,0,2);put(central,0,2);put(central,0,2);put(central,0,2);put(central,(long)0100644<<16,4);put(central,offset,4);central.write(name);
        }
        int offset=out.size();central.writeTo(out);put(out,0x06054b50,4);put(out,0,2);put(out,0,2);put(out,files.size(),2);put(out,files.size(),2);
        put(out,central.size(),4);put(out,offset,4);put(out,0,2);if(out.size()>ExtensionArtifacts.MAX_ZIP)throw ExtensionArtifacts.invalid();return out.toByteArray();
    }
}
